package handler

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/apps"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/tasks"
	"gorm.io/gorm"
)

// defaultSSHPort 未指定 SSH 端口时的默认值。
const defaultSSHPort = 22

// AppsHandler 应用商店处理器：内置应用目录查询与一键安装。
// 安装走 app_install 异步任务（在虚拟机内经 SSH 执行内置脚本，脚本正文见 service/apps）。
type AppsHandler struct {
	DB    *gorm.DB
	Tasks *tasks.Manager
	// VMCred 凭据托管（v3 批次 I）：use_saved / 未带口令时按 vm_id 取已保存凭据，
	// 明文只在服务端内存流转，task.payload 里只存凭据引用 ID。
	VMCred *VMCredentialHandler
}

// NewAppsHandler 创建应用商店处理器
func NewAppsHandler(db *gorm.DB, taskMgr *tasks.Manager) *AppsHandler {
	return &AppsHandler{DB: db, Tasks: taskMgr}
}

// SetVMCredentialHandler 注入凭据托管（渐进式，构造器签名保持不变）。
func (h *AppsHandler) SetVMCredentialHandler(ch *VMCredentialHandler) { h.VMCred = ch }

// List 应用目录。Detect/Install 无 json tag 不参与序列化，响应天然不含脚本正文。
func (h *AppsHandler) List(c *gin.Context) {
	Success(c, apps.All())
}

// Get 单应用详情：含检测/安装脚本预览（便于管理员安装前审查将在 VM 内执行的内容）。
func (h *AppsHandler) Get(c *gin.Context) {
	app, ok := apps.Get(c.Param("id"))
	if !ok {
		Fail(c, http.StatusNotFound, "应用不存在")
		return
	}
	Success(c, gin.H{
		"id":       app.ID,
		"name":     app.Name,
		"desc":     app.Desc,
		"category": app.Category,
		"detect":   app.Detect,
		"install":  app.Install,
	})
}

// Install 一键安装应用（异步）：校验 VM 存在性与授权后 Submit app_install 任务，
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
//
// 安全约束（P0 修复：本端点曾是「平台被当 SSH 跳板机」的漏网点）：
//  1. 外连目标必须过 validateSSHTarget —— 与 Web 终端 / 文件管理同一把白名单尺子：
//     已登记 IP 时目标必须与之精确一致，否则仅放行私有网段（排环回/组播/主机名）。
//     不校验的话，任意 operator 都能让服务器向任意内网 IP 发起 SSH（横扫/爆破/投脚本，
//     且源 IP 全算在服务器上）；
//  2. SSH 口令不得明文落库（tasks.payload 随任务入库）：优先走服务端凭据托管，
//     payload 只存 vm_credentials 的引用 ID；兼容「当场输入口令」的旧前端时，
//     在 HTTP 边界就地加密，payload 只存密文与盐（与凭据托管同一主密钥口径）。
//     主密钥未注入时一律 503 关闭（fail-closed：宁可装不了，也不落明文）。
//
// 兼容性：请求体仍收 host/port/user/password（旧前端照常工作），password 只是不再原样落库。
// 前端若传 use_saved=true（或不传 password）则完全不接触口令、走服务端凭据。
func (h *AppsHandler) Install(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusServiceUnavailable, "任务系统未初始化")
		return
	}
	var req struct {
		VMID     uint   `json:"vm_id"`
		AppID    string `json:"app_id"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		UseSaved bool   `json:"use_saved"` // true 时使用凭据托管（user/password 忽略）
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	// vm_id 经 JSON 绑定为 uint 后再交 GORM（与 paramID「先解析后查询」同一约束，
	// uint 走参数化条件，无注入面）；vm_id 在请求体而非路径中，故不经过 paramID。
	if req.VMID == 0 {
		Fail(c, http.StatusBadRequest, "虚拟机 ID 不能为空")
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, req.VMID).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	// 授权决定可见性：非 admin 未持有效授权与不存在同响应
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	app, ok := apps.Get(req.AppID)
	if !ok {
		Fail(c, http.StatusBadRequest, "应用不存在")
		return
	}
	// portGiven 记录请求是否显式给了端口（后面决定凭据登记端口要不要覆盖默认值）
	portGiven := req.Port != 0
	if req.Port == 0 {
		req.Port = defaultSSHPort
	}
	if req.Port < 1 || req.Port > 65535 {
		Fail(c, http.StatusBadRequest, "SSH 端口必须在 1-65535 之间")
		return
	}
	port := req.Port
	// 目标地址：请求未给则回落到 VM 已登记 IP（DHCP 租约 / guest agent 回填）
	host := strings.TrimSpace(req.Host)
	if host == "" {
		host = strings.TrimSpace(vm.IP)
	}
	if host == "" {
		Fail(c, http.StatusBadRequest, "请先同步该虚拟机的 IP，或填写其 SSH 地址")
		return
	}
	// 白名单收敛：与终端/文件管理同源（vmssh.ValidateTarget），错误文案本身就是面向用户的中文
	if err := validateSSHTarget(&vm, host, port); err != nil {
		log.Printf("[apps] 安装目标被拒 vm=%s(%d) app=%s target=%s:%d from=%s reason=%v",
			vm.Name, vm.ID, app.ID, host, port, c.ClientIP(), err)
		ErrorResponse(c, http.StatusBadRequest, err)
		return
	}

	// payload 只含非敏感字段：地址/端口/应用 ID + 凭据引用 ID（或边界加密后的密文与盐）
	payload := map[string]interface{}{
		"app_id": app.ID,
		"host":   host,
		"port":   port,
	}
	sshUser := ""
	if req.UseSaved || req.Password == "" {
		// 服务端凭据通道：明文口令不进请求处理链，更不落库
		if h.VMCred == nil {
			Fail(c, http.StatusServiceUnavailable, "凭据托管未初始化，请联系管理员配置主密钥后重启服务")
			return
		}
		credID, user, credPort, ok := h.VMCred.CredentialRefFor(c, vm.ID)
		if !ok {
			return
		}
		// 请求未显式给端口时以凭据登记端口为准（与 vm_files 的 use_saved 通道同口径）
		if credPort > 0 && !portGiven {
			port = credPort
			payload["port"] = port
		}
		payload["credential_id"] = credID
		sshUser = user
	} else {
		// 兼容通道：当场输入的口令在 HTTP 边界就地加密，落库的是密文（主密钥不在库里）
		if h.VMCred == nil || h.VMCred.MasterSecret == "" {
			Fail(c, http.StatusServiceUnavailable, "凭据加密未初始化，请联系管理员配置主密钥后重启服务")
			return
		}
		if req.User == "" {
			Fail(c, http.StatusBadRequest, "SSH 用户名不能为空")
			return
		}
		cipherB64, saltHex, err := secretbox.SealWithMaster(h.VMCred.MasterSecret, req.Password)
		if err != nil {
			ErrorWithMessage(c, http.StatusInternalServerError, "口令加密失败，请重试", err)
			return
		}
		payload["user"] = req.User
		payload["password_enc"] = cipherB64
		payload["salt"] = saltHex
		sshUser = req.User
	}

	// 留痕：谁、从哪、往哪台机器的哪个地址装了什么（口令与密文都不进日志）
	log.Printf("[apps] 提交安装任务 vm=%s(%d) app=%s target=%s:%d user=%s from=%s",
		vm.Name, vm.ID, app.ID, host, port, sshUser, c.ClientIP())
	userID, username := taskUserFromContext(c)
	vmID := vm.ID
	task, err := h.Tasks.Submit("app_install", "安装 "+app.Name+" 到 "+vm.Name,
		payload, userID, username, vm.Name, &vmID)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交安装任务失败", err)
		return
	}
	Accepted(c, "安装任务已提交", gin.H{"task_id": task.ID})
}
