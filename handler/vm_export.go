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

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/tasks"
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

	// 必须关机：运行中拷磁盘相当于拔盘复制，拿到的是文件系统不一致的坏镜像
	state, err := h.Virt.GetDomainState(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, fmt.Errorf("查询虚拟机状态失败（域可能未定义）: %w", err))
		return
	}
	if state != virt.StatusShutOff {
		Fail(c, http.StatusBadRequest, "请先关机再导出")
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
	exportName := strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '\r' || r == '\n' {
			return '_'
		}
		return r
	}, vm.Name+"-export.tar.gz")
	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", `attachment; filename="`+exportName+`"`)

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

// Import 从导出包恢复虚拟机（POST /api/vms/import-file，admin）。
// multipart 文件字段 file = Export 产出的 tar.gz。
func (h *VMExportHandler) Import(c *gin.Context) {
	// 路由挂在 /api/vms 组（OperatorMiddleware 对 operator 放行该前缀写操作），
	// 导入属平台资产变更，此处二次收口为仅 admin（同 vm_grant.go 的闸口模式）
	if !requireAdminRole(c) {
		return
	}

	// 20GB 上限：超限请求体被 MaxBytesReader 截断，multipart 解析随之报错
	//（防无上限上传把服务器临时目录/内存吃满）
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxVMImportBytes)
	fh, err := c.FormFile("file")
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest,
			"请上传导出包（multipart 字段 file，tar.gz 格式，最大 20GB）", err)
		return
	}

	// 落盘目标池：与创建虚拟机同一 resolver 钩子（设置页「默认存储池」可改）
	pool := tasks.DefaultStoragePoolResolver()
	poolPath, err := h.Virt.GetPoolPath(pool)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, fmt.Errorf("默认存储池 %s 不可用: %w", pool, err))
		return
	}

	// 导入宿主机：多宿主机尚未启用，首台即本机（同 vm_import.go 口径）。
	// 放在所有副作用之前校验——缺宿主机登记时直接失败，不产生任何需要回滚的状态。
	hostID, err := firstHostRecord(h.DB)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "请先在宿主机管理中登记宿主机", err)
		return
	}

	// 解包临时目录放在池路径下：与最终卷同一文件系统（rename 恢复秒级完成），
	// 也避开 /tmp 可能是 tmpfs 的问题（20GB 解包吃内存）；defer 兜底清理残留
	tmpDir, err := os.MkdirTemp(poolPath, ".vmops-import-")
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError,
			fmt.Errorf("在池 %s 创建解包临时目录失败: %w", pool, err))
		return
	}
	defer os.RemoveAll(tmpDir)

	src, err := fh.Open()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, fmt.Errorf("打开上传文件失败: %w", err))
		return
	}
	defer src.Close()

	xmlPath, diskPath, err := extractExportTarGz(src, tmpDir)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, fmt.Errorf("解包导出包失败: %w", err))
		return
	}
	xmlBytes, err := os.ReadFile(xmlPath)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, fmt.Errorf("读取包内 XML 失败: %w", err))
		return
	}

	spec, err := virt.ParseDomainXML(string(xmlBytes))
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, fmt.Errorf("导出包内 XML 解析失败: %w", err))
		return
	}
	if spec.Name == "" || spec.UUID == "" {
		Fail(c, http.StatusBadRequest, "导出包 XML 缺少 name/UUID，不是本平台导出的有效包")
		return
	}
	oldDiskPath := exportSystemDiskSource(spec)
	if oldDiskPath == "" {
		Fail(c, http.StatusBadRequest, "导出包 XML 中没有系统盘定义，无法导入")
		return
	}

	// 冲突检查（三重，全部通过才开始动池/域——失败时需要回滚的副作用越少越好）：
	// ① DB：同名或同 UUID 已存在（Unscoped 含软删回收站记录）
	var cnt int64
	if err := h.DB.Unscoped().Model(&model.VM{}).
		Where("uuid = ? OR name = ?", spec.UUID, spec.Name).
		Count(&cnt).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, fmt.Errorf("冲突检查查询数据库失败: %w", err))
		return
	}
	if cnt > 0 {
		Fail(c, http.StatusConflict, "同名/同 UUID 虚拟机已存在（可能在回收站）")
		return
	}
	// ② libvirt：同名域已定义（查询出错 = 域不存在 = 预期路径，无需留痕）
	if _, err := h.Virt.GetDomainState(spec.Name); err == nil {
		Fail(c, http.StatusConflict, "同名/同 UUID 虚拟机已存在（可能在回收站）")
		return
	}
	// ③ 池内同名卷文件已存在 → 拒绝。rename/拷贝都会静默覆盖，宁可拒导入也不损坏他人磁盘
	newVolPath := filepath.Join(poolPath, filepath.Base(diskPath))
	if _, err := os.Stat(newVolPath); err == nil {
		ErrorWithMessage(c, http.StatusConflict,
			"默认存储池中已存在同名卷文件（可能属于其他虚拟机），请先处理冲突后再导入",
			fmt.Errorf("目标卷已存在: %s", newVolPath))
		return
	}

	// 恢复卷：同文件系统 rename 瞬时完成；rename 失败（跨文件系统 EXDEV 等一律）
	// 回退 64KB 缓冲流式拷贝，不整载内存
	if err := restoreImportVolume(diskPath, newVolPath); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, fmt.Errorf("恢复磁盘卷失败: %w", err))
		return
	}

	// 修正 XML：包内 <source file=旧路径> → 新池路径。旧路径取自同一份 XML 的解析结果
	//（自洽，必然存在），做全量字符串替换；该路径在域 XML 中只出现在系统盘 source。
	// 已知边界：路径含 XML 转义字符（& < > 等）时原文替换不命中——平台生成的路径不含这些字符。
	fixedXML := strings.ReplaceAll(string(xmlBytes), oldDiskPath, newVolPath)

	// define（对应 virsh define，不启动）。失败清理已恢复的卷
	if err := h.Virt.DefineDomain(fixedXML); err != nil {
		h.cleanupImport(c, http.StatusInternalServerError, "导入虚拟机失败（定义域）",
			fmt.Errorf("define %s: %w", spec.Name, err), spec.Name, newVolPath, false)
		return
	}

	// 写 DB 记录。失败回滚 undefine + 删卷——宁可导入失败，不留「域有了库没有」的半套资产
	diskGB := h.Virt.DiskSizeGB(newVolPath) // 查不到（池未刷新等）返回 0，落库走 gorm default 20 兜底
	mac := ""
	if len(spec.Interfaces) > 0 {
		mac = spec.Interfaces[0].MAC
	}
	rec := model.VM{
		UUID:        spec.UUID,
		Name:        spec.Name,
		HostID:      hostID,
		StoragePool: pool,
		VCPU:        spec.VCPU,
		MemoryMB:    spec.MemoryMB,
		DiskGB:      diskGB,
		MACAddress:  mac,
		OSType:      spec.OSType,
		Status:      model.VMStatusShutOff, // 导入后即关机态，与 libvirt 实际一致
	}
	if err := h.DB.Create(&rec).Error; err != nil {
		// 完整 GORM 错误只进日志（防表结构/约束名泄漏），清理失败的域与卷
		h.cleanupImport(c, http.StatusInternalServerError, "导入虚拟机失败（写入数据库）",
			fmt.Errorf("写入 vms 表 %s: %w", spec.Name, err), spec.Name, newVolPath, true)
		return
	}

	log.Printf("[vm-export] 导入成功 vm=%s(%s) pool=%s vol=%s from=%s",
		rec.Name, rec.UUID, pool, newVolPath, c.ClientIP())
	Success(c, gin.H{
		"name":    rec.Name,
		"message": fmt.Sprintf("导入成功：虚拟机 %s 已恢复（关机状态），可在虚拟机列表中开机", rec.Name),
	})
}

// restoreImportVolume 把解包出的卷文件恢复到池内目标路径：优先 rename（同文件系统瞬时），
// 失败回退流式拷贝（跨文件系统）；拷贝后统一 0o644 权限（libvirt/qemu 读取）。
func restoreImportVolume(tmpVol, newVolPath string) error {
	if err := os.Rename(tmpVol, newVolPath); err == nil {
		return os.Chmod(newVolPath, 0o644)
	}
	// rename 失败一律回退拷贝（主因是跨文件系统 EXDEV，不区分错误类型以兼容特殊挂载）
	if err := copyFileSync(tmpVol, newVolPath); err != nil {
		return err
	}
	return os.Chmod(newVolPath, 0o644)
}

// copyFileSync 流式拷贝文件（64KB 缓冲），失败时清理半成品目标文件——
// 否则残卷会让后续导入被「卷已存在」守卫挡住，还要人工清理。
func copyFileSync(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开源文件 %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("创建目标文件 %s: %w", dst, err)
	}
	_, cpErr := io.CopyBuffer(out, in, make([]byte, tarCopyBufSize))
	if cerr := out.Close(); cpErr == nil {
		cpErr = cerr
	}
	if cpErr != nil {
		if rmErr := os.Remove(dst); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("[vm-export] 清理拷贝半成品 %s 失败（需人工处理）: %v", dst, rmErr)
		}
		return fmt.Errorf("拷贝 %s → %s: %w", src, dst, cpErr)
	}
	return nil
}

// cleanupImport 导入失败清理：按副作用产生顺序逆序撤销（undefine → 删卷）。
// 清理自身的失败只留日志（标注「需人工清理」），不覆盖响应中的主错误；卷不存在视为已清理。
func (h *VMExportHandler) cleanupImport(c *gin.Context, status int, msg string, err error, domainName, volPath string, defined bool) {
	if defined && domainName != "" {
		if uerr := h.Virt.UndefineDomain(domainName); uerr != nil {
			LogError(c, fmt.Errorf("导入失败回滚：undefine %s 失败（需人工清理）: %w", domainName, uerr))
		}
	}
	if volPath != "" {
		if rerr := os.Remove(volPath); rerr != nil && !os.IsNotExist(rerr) {
			LogError(c, fmt.Errorf("导入失败回滚：删除卷 %s 失败（需人工清理）: %w", volPath, rerr))
		}
	}
	ErrorWithMessage(c, status, msg, err)
}

// extractExportTarGz 流式解包 tar.gz 到 dstDir，返回包内 domain XML 与磁盘卷的临时文件路径。
// 只解出第一个 .xml 与第一个 .qcow2 成员（本平台导出包固定两成员），其余成员丢弃推进游标；
// 条目名取 filepath.Base 并只接受普通文件（防 tar 路径穿越与符号链接逃逸）。
func extractExportTarGz(r io.Reader, dstDir string) (xmlPath, diskPath string, err error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return "", "", fmt.Errorf("不是有效的 gzip 流: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)

	var xmlName, diskName string
	for i := 0; i < maxImportEntries; i++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", fmt.Errorf("读取 tar 条目: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue // 目录/符号链接/硬链接一律跳过（符号链接逃逸面）
		}
		name := filepath.Base(hdr.Name) // 只取基名：../ 穿越与绝对路径在此被拍平
		switch {
		case xmlPath == "" && strings.HasSuffix(strings.ToLower(name), ".xml"):
			p, e := extractTarEntry(tr, filepath.Join(dstDir, name), maxImportXMLBytes)
			if e != nil {
				return "", "", e
			}
			xmlPath, xmlName = p, name
		case diskPath == "" && strings.HasSuffix(strings.ToLower(name), ".qcow2"):
			p, e := extractTarEntry(tr, filepath.Join(dstDir, name), 0)
			if e != nil {
				return "", "", e
			}
			diskPath, diskName = p, name
		default:
			// 无关成员：丢弃内容推进解压游标（不写盘）
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return "", "", fmt.Errorf("跳过 tar 条目 %s: %w", name, err)
			}
		}
		if xmlPath != "" && diskPath != "" {
			break
		}
	}
	if xmlPath == "" || diskPath == "" {
		return "", "", errors.New("包内未找到 .xml 与 .qcow2 成员（不是本平台导出的包）")
	}
	// 配对校验：xml 名与 qcow2 名需同词干或前缀相属（<域名>.xml ↔ <域名>-dN.qcow2），
	// 防止包内两个不相干文件被凑成一对导入
	xs, ds := tarExportStem(xmlName), tarExportStem(diskName)
	if xs != ds && !strings.HasPrefix(ds, xs) && !strings.HasPrefix(xs, ds) {
		return "", "", fmt.Errorf("包内 XML（%s）与磁盘文件（%s）名称不匹配", xmlName, diskName)
	}
	return xmlPath, diskPath, nil
}

// extractTarEntry 把当前 tar 条目流式写入 dstPath。maxBytes>0 时超限即报错
// （防超大伪 XML 撑爆临时目录）；maxBytes==0 不限（磁盘卷本身可达数十 GB）。
func extractTarEntry(tr *tar.Reader, dstPath string, maxBytes int64) (string, error) {
	src := io.Reader(tr)
	if maxBytes > 0 {
		// 多读 1 字节用于触顶判定：写满 maxBytes+1 即说明真实内容超限
		src = io.LimitReader(tr, maxBytes+1)
	}
	f, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("创建解包临时文件 %s: %w", filepath.Base(dstPath), err)
	}
	n, werr := io.CopyBuffer(f, src, make([]byte, tarCopyBufSize))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil && maxBytes > 0 && n > maxBytes {
		werr = fmt.Errorf("超过大小上限 %d 字节", maxBytes)
	}
	if werr != nil {
		if rmErr := os.Remove(dstPath); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("[vm-export] 清理解包临时文件 %s 失败: %v", dstPath, rmErr)
		}
		return "", fmt.Errorf("解包 %s: %w", filepath.Base(dstPath), werr)
	}
	return dstPath, nil
}

// firstHostRecord 返回平台登记的首台宿主机 ID（导入目标；多宿主机尚未启用，首台即本机）。
func firstHostRecord(db *gorm.DB) (uint, error) {
	var host model.Host
	if err := db.Order("id ASC").First(&host).Error; err != nil {
		return 0, fmt.Errorf("查询宿主机: %w", err)
	}
	return host.ID, nil
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
