package handler

// vm_export.go v3 批次 J：虚拟机导出/导入（tar.gz 全量包 = 域 XML + 系统盘卷）。
//
// 导出（GET /api/vms/:id/export）：VM 必须关机；把 GetDomainSpec 的 RawXML 与
// 系统盘卷文件流式打成 tar.gz 直接写响应体——gzip/tar 两级 writer 管道到 HTTP，
// 64KB 缓冲分块拷贝，全程不落临时盘、不整载内存，20GB 级磁盘不放大进程内存。
//
// 导入（POST /api/vms/import-file，admin）：multipart 上传导出包，Go 标准库
// multipart 解析超过 MaxMultipartMemory（main.go 设 32MB）的部分自动落服务器
// 临时文件，随后逐 tar 条目流式解包到池内临时目录；卷恢复用 rename（同文件系统）
// 回退 io.Copy（跨文件系统），域 XML 里的旧磁盘路径替换为新池路径后 define，
// 最后写 vms 表记录。任一步失败按已产生的副作用逆序清理（undefine + 删卷）。
//
// 包格式（Export 产出，Import 消费）：
//	<域名>.xml    —— virsh dumpxml 原文
//	<盘名>.qcow2  —— 系统盘卷文件原始字节（device=='disk' 的第一块）
// 数据盘/cdrom 不在包内，见文件尾「已知限制」。
//
// 本文件（vm_export.go）管导出侧（流式打包）与导入包格式的公共命名规则；
// 导入侧（解包/恢复/回滚）见 vm_export_import.go。

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// VMExportHandler 虚拟机导出/导入处理器。
type VMExportHandler struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewVMExportHandler 创建导出/导入处理器（Virt 由 Deps 注入，与全局共享同一连接实例）。
func NewVMExportHandler(db *gorm.DB, v *virt.Virt) *VMExportHandler {
	return &VMExportHandler{DB: db, Virt: v}
}

// 限额常量。
const (
	maxVMImportBytes  = 20 << 30 // 导入包（tar.gz）请求体上限 20GB
	maxImportXMLBytes = 4 << 20  // 包内 domain XML 大小上限（域 XML 正常几十 KB）
	maxImportEntries  = 64       // 最多扫描的 tar 条目数（只需 xml+qcow2 两件，防恶意超长包）
	tarCopyBufSize    = 64 << 10 // tar 流式拷贝缓冲 64KB
)

// Export 导出虚拟机为 tar.gz（GET /api/vms/:id/export）。
// 响应体即文件流（application/gzip, attachment），不套 {code,message,data} 信封
// ——与 vm_files.go 的 Download 同属文件下载边界。
func (h *VMExportHandler) Export(c *gin.Context) {
	// 导出含完整磁盘镜像，属数据面重操作：viewer 只读角色不可用。
	// vms 组的 OperatorMiddleware 对 GET 全放行（viewer 也能到这里），
	// 故在 handler 内收紧为 operator/admin（与需求「GET 挂 vms 组（operator）」对齐）。
	roleRaw, _ := c.Get("role")
	switch role, _ := roleRaw.(string); role {
	case "admin", "operator":
	default:
		Fail(c, http.StatusForbidden, "导出虚拟机需要操作员或管理员权限")
		return
	}

	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	// 授权决定可见性：非 admin 未持有效授权与不存在同响应（不泄露存在性）
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}

	// 导出全程持锁：流式拷贝历时数分钟（20GB 级），期间 Start/Delete/Clone 若并发进入——
	// 开机会拷出文件系统不一致的坏镜像，delete_vm 会直接截断正在读的盘文件。锁在
	// handler 返回（流写完或出错）时释放，天然覆盖全程；先 guard 挡已提交未完结任务。
	if !guardVMIdle(c, h.DB, vm.ID) {
		return
	}
	release, ok := lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()

	// 必须关机：运行中拷磁盘相当于拔盘复制，拿到的是文件系统不一致的坏镜像
	state, err := h.Virt.GetDomainState(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, fmt.Errorf("查询虚拟机状态失败（域可能未定义）: %w", err))
		return
	}
	if state != virt.StatusShutOff {
		Fail(c, http.StatusConflict, "请先关机再导出")
		return
	}

	// GetDomainSpec 一次拿全：RawXML（dumpxml 原文，直接进包）+ 磁盘列表（定位系统盘）。
	// VM 已关机，运行时 XML 与持久定义一致，无需另取 INACTIVE XML。
	spec, err := h.Virt.GetDomainSpec(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, fmt.Errorf("获取虚拟机配置失败: %w", err))
		return
	}
	if spec.RawXML == "" {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取虚拟机 XML 为空，无法导出",
			fmt.Errorf("GetDomainSpec 返回空 RawXML vm=%s(%d)", vm.Name, vm.ID))
		return
	}
	diskPath := exportSystemDiskSource(spec)
	if diskPath == "" {
		Fail(c, http.StatusBadRequest, "未找到可导出的系统盘")
		return
	}
	fi, err := os.Stat(diskPath)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "系统盘文件不存在或不可访问，无法导出",
			fmt.Errorf("os.Stat %s: %w", diskPath, err))
		return
	}
	if fi.IsDir() {
		ErrorWithMessage(c, http.StatusInternalServerError, "系统盘路径是目录而非镜像文件，无法导出",
			fmt.Errorf("系统盘路径是目录: %s", diskPath))
		return
	}

	// 响应头先于任何字节写出；此后再出错已无法改状态码，只能留日志（客户端表现为流截断）。
	// 文件名进 HTTP 头，替换引号/换行/反斜杠防响应头被破坏（同 vm_files.go Download）。
	c.Header("Content-Type", "application/gzip")
	setAttachmentHeader(c, vm.Name+"-export.tar.gz")

	if err := streamExportTarGz(c.Writer, spec, diskPath); err != nil {
		LogError(c, fmt.Errorf("导出流写入中断 vm=%s(%d) disk=%s: %w", vm.Name, vm.ID, diskPath, err))
	}
}

// streamExportTarGz 把域 XML 与系统盘流式打包成 tar.gz 写入 w。
// 磁盘内容经 64KB 缓冲分块拷贝（io.CopyBuffer），任何中间体积都不驻留内存或临时文件。
func streamExportTarGz(w io.Writer, spec *virt.DomainSpec, diskPath string) error {
	// BestSpeed：qcow2 空洞/零块高度可压，压缩率仍然可观，但 CPU 时间比默认档低一档，
	// 20GB 级导出不至于把单核跑满数分钟
	gzw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return fmt.Errorf("创建 gzip writer 失败: %w", err)
	}
	tw := tar.NewWriter(gzw)
	now := time.Now()

	// 成员一：域 XML。PAX 格式兼容非 ASCII 域名（USTAR 对UTF-8 文件名无能为力）
	xmlEntry := spec.Name + ".xml"
	hdr := &tar.Header{
		Name:    xmlEntry,
		Mode:    0o644,
		Size:    int64(len(spec.RawXML)),
		ModTime: now,
		Format:  tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("写 tar 头 %s 失败: %w", xmlEntry, err)
	}
	if _, err := io.WriteString(tw, spec.RawXML); err != nil {
		return fmt.Errorf("写 tar 内容 %s 失败: %w", xmlEntry, err)
	}

	// 成员二：系统盘卷文件
	f, err := os.Open(diskPath)
	if err != nil {
		return fmt.Errorf("打开系统盘 %s 失败: %w", diskPath, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("获取系统盘大小 %s 失败: %w", diskPath, err)
	}
	diskEntry := tarExportDiskName(diskPath)
	hdr = &tar.Header{
		Name:    diskEntry,
		Mode:    0o644,
		Size:    fi.Size(),
		ModTime: now,
		Format:  tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("写 tar 头 %s 失败: %w", diskEntry, err)
	}
	if _, err := io.CopyBuffer(tw, f, make([]byte, tarCopyBufSize)); err != nil {
		return fmt.Errorf("流式写入磁盘卷 %s 失败: %w", diskEntry, err)
	}

	// 收尾顺序：tar 先 flush 结束块，gzip 再 flush 尾部；任一 Close 失败都意味着流不完整
	if err := tw.Close(); err != nil {
		return fmt.Errorf("收尾 tar 流失败: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return fmt.Errorf("收尾 gzip 流失败: %w", err)
	}
	return nil
}

// exportSystemDiskSource 返回 spec 中首个 device=='disk' 且有源路径的磁盘路径（系统盘），无则空串。
// 与 virt 包未导出的 systemDiskIndex 同一定位规则（cdrom/floppy 不算系统盘），改动需两处同步。
func exportSystemDiskSource(spec *virt.DomainSpec) string {
	for i := range spec.Disks {
		if spec.Disks[i].Device == "disk" && spec.Disks[i].Source != "" {
			return spec.Disks[i].Source
		}
	}
	return ""
}

// tarExportDiskName 由磁盘源路径推导 tar 内的卷文件名：去扩展名后统一补 .qcow2。
// 平台系统盘本就是 qcow2；raw 盘内容原样打包仅文件名规范化（qemu 以 XML 的
// driver type 识别格式而非扩展名，导入后可正常启动）。
func tarExportDiskName(diskPath string) string {
	return tarExportStem(filepath.Base(diskPath)) + ".qcow2"
}

// tarExportStem 取文件名去扩展名后的词干（xml 与 qcow2 成员配对比较用）。
func tarExportStem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// 已知限制（按需在文档中披露，勿宣称已解决）：
//   - 导出包只含系统盘（device=='disk' 的第一块）；数据盘/cdrom 不在包内，
//     导入后 XML 里数据盘仍指向旧路径——同宿主机且旧文件还在时可继续挂载，
//     跨机导入需手工补盘，否则开机报磁盘缺失。
//   - 导出含 backing file 的增量盘时只打包叶子卷，父盘链不在包内，跨机导入后链断裂；
//     本机导入不受影响（父盘仍在原位）。带增量链的 VM 导出前建议先做快照合并/全量克隆。
//   - XML 磁盘路径替换按原文匹配，路径含 XML 转义字符（& < >）时不命中
//     （平台生成的路径不含这些字符，仅外部构造的包可能触发）。
//   - multipart 上传的溢出临时文件由 Go 标准库落在 os.TempDir()（通常 /tmp），
//     /tmp 为 tmpfs 的部署上传 20GB 会占内存——属部署形态约束，代码内无法规避。
