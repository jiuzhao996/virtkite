package handler

// vm_export_import.go 虚拟机导入侧：multipart 上传导出包、流式解包 tar.gz、恢复系统盘卷、
// 修正 XML 后 define 并落库，失败按副作用逆序回滚（undefine + 删卷）。
// 导出侧与导入包格式（限额常量、tar 命名规则）见 vm_export.go。

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

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

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
