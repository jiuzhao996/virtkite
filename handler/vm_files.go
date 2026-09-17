package handler

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"github.com/jiuzhao/vmops/service/vmssh"
	"gorm.io/gorm"
)

// VMFilesHandler 虚拟机文件管理处理器：经 SSH 在 VM 内执行列目录/下载/上传/删除/建目录。
//
// 与 Web 终端同一信任模型：SSH 拨号参数（host/port/user/password）由前端请求体携带、
// 仅内存透传不落盘，目标必须先过 validateSSHTarget 白名单（防平台沦为跳板机）；
// VM 内的一切路径参数必须经 vmssh.ShellQuote 包裹后才能拼进远程命令（防命令注入）。
type VMFilesHandler struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewVMFilesHandler 创建虚拟机文件管理处理器（构造风格对齐 vnc.go：Virt 内部自建，惰性连接）。
func NewVMFilesHandler(db *gorm.DB) *VMFilesHandler {
	return &VMFilesHandler{DB: db, Virt: virt.New()}
}

// maxVMFileUpload 单文件上传的解码后大小上限。
// SSH exec 通道会把内容整体载入内存，必须设上限防大文件 OOM；
// 大文件/二进制应走离线挂载通道（guestmount，见 OfflineCapability，后续接入）。
const maxVMFileUpload = 16 << 20 // 16MB

// vmFilesReq 文件操作统一请求体（各端点按需取用字段）。
type vmFilesReq struct {
	Host     string   `json:"host"`     // VM 的 IP
	Port     int      `json:"port"`     // SSH 端口，省略 = 22
	User     string   `json:"user"`     // SSH 用户名
	Password string   `json:"password"` // SSH 密码（不落盘不进日志）
	Path     string   `json:"path"`     // 远程路径（List/Download/Upload/Mkdir）
	Paths    []string `json:"paths"`    // 批量路径（Delete）
	Content  string   `json:"content"`  // 文件内容（Upload，base64 编码）
}

// resolve 文件操作的公共前置：解析 :id → 取 VM → 授权可见性 → 绑定请求体 →
// SSH 拨号目标白名单校验。任一步失败已写好响应，调用方直接 return。
func (h *VMFilesHandler) resolve(c *gin.Context, req *vmFilesReq, op string) (vm *model.VM, opts vmssh.Options, ok bool) {
	id, ok := paramID(c, "id")
	if !ok {
		return nil, vmssh.Options{}, false
	}
	var record model.VM
	if err := h.DB.First(&record, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return nil, vmssh.Options{}, false
	}
	// 授权决定可见性：非 admin 未持有效授权与不存在同响应（不泄露存在性）
	if !vmVisible(c, h.DB, record.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return nil, vmssh.Options{}, false
	}
	if err := c.ShouldBindJSON(req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误（需提供主机、用户名和密码）", err)
		return nil, vmssh.Options{}, false
	}
	if req.Host == "" || req.User == "" || req.Password == "" {
		Fail(c, http.StatusBadRequest, "请填写主机、用户名和密码")
		return nil, vmssh.Options{}, false
	}
	port := req.Port
	if port == 0 {
		port = 22
	}
	// 复用终端的目标白名单：已记录 IP 精确匹配，否则仅放行 RFC1918 私有网段（排环回/组播）
	if err := validateSSHTarget(&record, req.Host, port); err != nil {
		// 其错误文案本就是面向用户的中文白名单说明（见 terminal.go），经 friendlyMessage 安全透出
		log.Printf("[vm-files] 目标被拒 op=%s vm=%s(%d) target=%s:%d user=%s from=%s reason=%v",
			op, record.Name, record.ID, req.Host, port, req.User, c.ClientIP(), err)
		ErrorResponse(c, http.StatusBadRequest, err)
		return nil, vmssh.Options{}, false
	}
	// 留痕：谁、从哪、对哪个目标做了什么（口令不记录）
	log.Printf("[vm-files] %s vm=%s(%d) target=%s:%d user=%s from=%s",
		op, record.Name, record.ID, req.Host, port, req.User, c.ClientIP())
	return &record, vmssh.Options{Host: req.Host, Port: port, User: req.User, Password: req.Password}, true
}

// checkPath 远程路径合法性：非空且以 / 开头（拒绝相对路径，避免语义随登录 shell 起始目录漂移）。
func checkPath(c *gin.Context, p string) bool {
	if strings.TrimSpace(p) == "" {
		Fail(c, http.StatusBadRequest, "路径不能为空")
		return false
	}
	if !strings.HasPrefix(p, "/") {
		Fail(c, http.StatusBadRequest, "路径必须以 / 开头")
		return false
	}
	return true
}

// vmFileItem 目录条目（与前端约定字段）。
type vmFileItem struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`     // 如 drwxr-xr-x / -rw-r--r--
	Modified int64  `json:"modified"` // 秒级 Unix 时间戳
	IsDir    bool   `json:"is_dir"`
}

// List 列出 VM 内目录（POST /api/vms/:id/files/list）。
// body: {host, port?, user, password, path?}；path 空默认 /root。
func (h *VMFilesHandler) List(c *gin.Context) {
	req := &vmFilesReq{}
	_, opts, ok := h.resolve(c, req, "列目录")
	if !ok {
		return
	}
	p := req.Path
	if strings.TrimSpace(p) == "" {
		p = "/root"
	}
	if !checkPath(c, p) {
		return
	}
	// --time-style=+%s 让时间列输出秒级时间戳：格式固定可解析，且不受远程 locale 影响
	stdout, stderr, err := vmssh.Run(opts, "ls -la --time-style=+%s "+vmssh.ShellQuote(p), vmssh.DefaultTimeout)
	if err != nil {
		// stderr（远程 ls 原文）只进服务端日志，前端收固定文案
		ErrorWithMessage(c, http.StatusBadRequest, "读取目录失败，请确认虚拟机已开机、SSH 服务可用且路径存在",
			fmt.Errorf("ls %s 失败: %w（stderr: %s）", p, err, stderr))
		return
	}
	Success(c, gin.H{"path": p, "items": parseLsOutput(stdout)})
}

// Download 读取 VM 内文件内容（POST /api/vms/:id/files/download）。
// body: {host, port?, user, password, path}；响应体即文件内容（application/octet-stream）。
//
// ⚠️ 通道能力边界：stdout 经 SSH exec 全量载入内存后透传，适合文本/配置等小文件；
// 二进制与大文件不建议走本通道（无分块/断点，大文件占内存），应使用离线挂载通道
// （guestmount，见 OfflineCapability，后续接入）。stdout 与 stderr 分开接收，
// 远程报错不会混入文件内容。
func (h *VMFilesHandler) Download(c *gin.Context) {
	req := &vmFilesReq{}
	_, opts, ok := h.resolve(c, req, "下载文件")
	if !ok {
		return
	}
	if !checkPath(c, req.Path) {
		return
	}
	stdout, stderr, err := vmssh.Run(opts, "cat "+vmssh.ShellQuote(req.Path), vmssh.DefaultTimeout)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "读取文件失败，请确认路径存在且是普通文件",
			fmt.Errorf("cat %s 失败: %w（stderr: %s）", req.Path, err, stderr))
		return
	}
	name := path.Base(req.Path)
	if name == "/" || name == "." || name == "" {
		name = "download"
	}
	// 文件名进 HTTP 头，替换引号防响应头被破坏
	name = strings.ReplaceAll(name, `"`, `_`)
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "application/octet-stream", []byte(stdout))
}

// Upload 写文件到 VM（POST /api/vms/:id/files/upload）。
// body: {host, port?, user, password, path, content(base64)}；经 session stdin 喂给 cat > 路径。
func (h *VMFilesHandler) Upload(c *gin.Context) {
	req := &vmFilesReq{}
	_, opts, ok := h.resolve(c, req, "上传文件")
	if !ok {
		return
	}
	if !checkPath(c, req.Path) {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "content 不是合法的 base64 编码", err)
		return
	}
	if len(raw) > maxVMFileUpload {
		Fail(c, http.StatusBadRequest, "文件超过大小上限（16MB），请改用离线挂载通道传输大文件")
		return
	}
	_, stderr, err := vmssh.RunWithStdin(opts, "cat > "+vmssh.ShellQuote(req.Path), bytes.NewReader(raw), vmssh.DefaultTimeout)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "写入文件失败，请确认目标目录存在且有写权限",
			fmt.Errorf("cat > %s 失败: %w（stderr: %s）", req.Path, err, stderr))
		return
	}
	Success(c, gin.H{"path": req.Path, "size": len(raw)})
}

// Delete 批量删除 VM 内文件/目录（POST /api/vms/:id/files/delete）。
// body: {host, port?, user, password, paths: ["/a","/b"]}；逐个 ShellQuote 后拼 rm -rf -- 执行。
func (h *VMFilesHandler) Delete(c *gin.Context) {
	req := &vmFilesReq{}
	_, opts, ok := h.resolve(c, req, "删除文件")
	if !ok {
		return
	}
	if len(req.Paths) == 0 {
		Fail(c, http.StatusBadRequest, "paths 不能为空")
		return
	}
	quoted := make([]string, 0, len(req.Paths))
	for _, p := range req.Paths {
		if !checkPath(c, p) {
			return
		}
		quoted = append(quoted, vmssh.ShellQuote(p))
	}
	// rm -rf --：-- 终结选项解析，路径即使以 - 开头也不会被当成选项
	_, stderr, err := vmssh.Run(opts, "rm -rf -- "+strings.Join(quoted, " "), vmssh.DefaultTimeout)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "删除失败，请确认路径存在且有权限",
			fmt.Errorf("rm 失败: %w（stderr: %s）", err, stderr))
		return
	}
	Success(c, gin.H{"deleted": len(quoted)})
}

// Mkdir 在 VM 内建目录（POST /api/vms/:id/files/mkdir）。
// body: {host, port?, user, password, path}；mkdir -p 逐级创建、已存在不报错。
func (h *VMFilesHandler) Mkdir(c *gin.Context) {
	req := &vmFilesReq{}
	_, opts, ok := h.resolve(c, req, "创建目录")
	if !ok {
		return
	}
	if !checkPath(c, req.Path) {
		return
	}
	_, stderr, err := vmssh.Run(opts, "mkdir -p "+vmssh.ShellQuote(req.Path), vmssh.DefaultTimeout)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "创建目录失败，请确认父目录有写权限",
			fmt.Errorf("mkdir %s 失败: %w（stderr: %s）", req.Path, err, stderr))
		return
	}
	Success(c, gin.H{"path": req.Path})
}

// OfflineCapability 离线文件通道就绪度探测（GET /api/vms/:id/files/offline-capability）。
// 只探测不执行挂载：guestmount 是否安装 + VM 是否关机（关机才可离线挂载其磁盘）。
func (h *VMFilesHandler) OfflineCapability(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}

	// 探测宿主机是否安装 guestmount（libguestfs 套件，对应 which guestmount）
	guestmountAvailable := false
	if p, err := exec.LookPath("guestmount"); err == nil && p != "" {
		guestmountAvailable = true
	}

	// VM 必须关机才允许离线挂载磁盘（运行中挂载有文件系统损坏风险）
	vmShutoff := false
	state, err := h.Virt.GetDomainState(vm.Name)
	if err != nil {
		// 状态查询失败按「未关机」处理（fail-closed），留痕供排查
		log.Printf("[vm-files] 离线通道探测查询状态失败 vm=%s(%d) err=%v", vm.Name, vm.ID, err)
	} else {
		vmShutoff = state == virt.StatusShutOff
	}

	Success(c, gin.H{
		"guestmount_available": guestmountAvailable,
		"vm_shutoff":           vmShutoff,
		"ready":                guestmountAvailable && vmShutoff,
	})
}

// parseLsOutput 解析 `ls -la --time-style=+%s` 的输出（跳过 total 行与 . .. 条目）。
// 返回值恒为非 nil 空切片，保证 JSON 序列化为 [] 而非 null。
func parseLsOutput(out string) []vmFileItem {
	items := []vmFileItem{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "total ") {
			continue
		}
		it, ok := parseLsLine(line)
		if !ok {
			continue
		}
		if it.Name == "." || it.Name == ".." {
			continue
		}
		items = append(items, it)
	}
	return items
}

// parseLsLine 解析单行 ls 输出。列布局（--time-style=+%s 下共 7 列起）：
//
//	模式      链接数 属主  属组  大小  时间戳  名称...
//	drwxr-xr-x 1     root  root  4096  1694000000 dir
//
// 文件名可含空格，取第 7 列起重新拼回；符号链接带「 -> 目标」后缀，只取链接名。
// 已知取舍：文件名中的连续空格会被 Fields 折叠、含换行的文件名无法解析——属 ls 文本
// 协议固有局限，管理平台场景可接受。
func parseLsLine(line string) (vmFileItem, bool) {
	fields := strings.Fields(line)
	if len(fields) < 7 {
		return vmFileItem{}, false
	}
	mode := fields[0]
	if mode == "" {
		return vmFileItem{}, false
	}
	size, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return vmFileItem{}, false
	}
	modified, err := strconv.ParseInt(fields[5], 10, 64)
	if err != nil {
		return vmFileItem{}, false
	}
	name := strings.Join(fields[6:], " ")
	if i := strings.Index(name, " -> "); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return vmFileItem{}, false
	}
	return vmFileItem{Name: name, Size: size, Mode: mode, Modified: modified, IsDir: mode[0] == 'd'}, true
}
