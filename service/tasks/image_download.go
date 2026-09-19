// image_download.go：云镜像下载 executor（v3 批次 H「云镜像市场」）。
//
// 从官方发行版站点流式下载云镜像（qcow2）到指定存储池目录，完成后登记 images 表
// （等价手工 curl -L -o <池目录>/<文件名> + 镜像库登记，libvirt 无对应 virsh 命令——
// 落盘到目录池路径即被池扫描识别，与 handler.UploadImage 同一口径）。
// 约束：executor 运行在 worker goroutine，禁止引用 gin/handler；
// 错误一律 fmt.Errorf("中文描述: %w", err) 保留错误链（完整链由 manager.run 打日志）。
package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// 云镜像下载任务常量。
const (
	// imageDownloadTimeout 单次下载整体超时（含建连、重定向与响应体读取）：
	// 数百 MB 级云镜像按 ~1MB/s 兜底约 10 分钟，30 分钟留足余量防悬挂占死 worker。
	imageDownloadTimeout = 30 * time.Minute
	// imageDownloadMaxRedirects 重定向上限：官方源常 302 到镜像站（实测 Debian → saimei.ftp.acc.umu.se）。
	imageDownloadMaxRedirects = 5
	// imageDownloadPartSuffix 临时文件后缀：下载中写 target+.part，完成后原子 rename，
	// 半截文件不会被误登记为可用镜像；中断后重跑覆盖 .part 再 rename。
	imageDownloadPartSuffix = ".part"
	// imageProgressCeil 下载阶段进度上限：100 留给 manager 的 success 终态统一写。
	imageProgressCeil = 95
)

// RegisterImageDownload 注册 image_download executor（云镜像市场下载）。
func RegisterImageDownload(m *Manager) {
	if m == nil {
		return
	}
	m.Register("image_download", execImageDownload)
}

// execImageDownload 下载云镜像到存储池并登记镜像库。
// payload：{url*（必须 https）, name（展示名，仅进结果描述）, pool*（缺省走 DefaultStoragePoolResolver）}。
func execImageDownload(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}
	payload := ctx.Payload

	rawURL, ok := strParam(payload, "url")
	if !ok || rawURL == "" {
		return errors.New("缺少下载地址参数")
	}
	// 仅接受 https 明文拒绝：镜像站点全部支持 https，防降级明文传输被中间人篡改磁盘内容
	if !strings.HasPrefix(rawURL, "https://") {
		return errors.New("下载地址必须以 https:// 开头")
	}
	name, _ := strParam(payload, "name")
	pool, _ := strParam(payload, "pool")
	if pool == "" {
		pool = DefaultStoragePoolResolver()
	}

	// 1) 解析池目录并确保存在（目录池按路径扫描见卷，目录尚未落盘时补建）
	poolPath, err := ctx.Virt.GetPoolPath(pool)
	if err != nil {
		return fmt.Errorf("获取存储池 %s 路径失败: %w", pool, err)
	}
	if err := os.MkdirAll(poolPath, 0o755); err != nil {
		return fmt.Errorf("创建存储池目录失败: %w", err)
	}

	// 2) 目标文件 = 池目录/URL 尾段（清单 URL 均以真实文件名结尾）
	fileName, err := FileNameFromURL(rawURL)
	if err != nil {
		return err
	}
	target := filepath.Join(poolPath, fileName)

	// 3) 文件已存在（非空常规文件）→ 跳过下载直接登记（幂等重跑，不重复耗流量）
	if st, statErr := os.Stat(target); statErr == nil && st.Mode().IsRegular() && st.Size() > 0 {
		reportProgress(ctx, 90, "目标文件已存在，跳过下载")
		return registerImageRecord(ctx, target, name, pool, true)
	}

	// 4) 流式下载到 .part → 原子 rename
	reportProgress(ctx, 2, "开始下载 "+fileName)
	if err := downloadToFile(ctx, rawURL, target); err != nil {
		return err
	}
	reportProgress(ctx, 96, "下载完成，正在登记镜像库")
	return registerImageRecord(ctx, target, name, pool, false)
}

// FileNameFromURL 取 URL 路径尾段作为落盘文件名（清洗到安全字符集，纯函数供单测）。
func FileNameFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("下载地址无法解析: %w", err)
	}
	seg := u.Path
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	// 解码失败保留原值：只影响展示/落盘名，不影响下载本身
	if dec, decErr := url.PathUnescape(seg); decErr == nil {
		seg = dec
	}
	seg = sanitizeDownloadFileName(seg)
	if seg == "" || seg == "." || seg == ".." {
		return "", errors.New("无法从下载地址识别文件名")
	}
	return seg, nil
}

// sanitizeDownloadFileName 仅保留文件名安全字符（与 handler.sanitizeFileName 同一字符集；
// tasks 包禁 import handler，故自实现一份——两处字符集必须同步修改）。
func sanitizeDownloadFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// downloadToFile 流式下载 rawURL 到 target：写 target+.part，成功后原子 rename。
// Client 整体超时 30 分钟；重定向最多 5 次，且不允许重定向降级到非 https。
func downloadToFile(ctx *ExecContext, rawURL, target string) error {
	client := &http.Client{
		Timeout: imageDownloadTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= imageDownloadMaxRedirects {
				return fmt.Errorf("重定向超过 %d 次", imageDownloadMaxRedirects)
			}
			if req.URL.Scheme != "https" {
				return errors.New("重定向降级到非 https，已中止")
			}
			return nil
		},
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		// CheckRedirect 的错误也从这里出来（包一层中文前缀，friendlyError 才有中文可取）
		return fmt.Errorf("发起下载请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载服务器返回异常状态码 %d", resp.StatusCode)
	}

	partPath := target + imageDownloadPartSuffix
	f, err := os.Create(partPath)
	if err != nil {
		return fmt.Errorf("创建临时下载文件失败: %w", err)
	}
	pr := &downloadProgressWriter{ctx: ctx, total: resp.ContentLength}
	// MultiWriter 边落盘边计字节数；Copy 的 32KB 内置缓冲足够，无需 CopyBuffer
	_, copyErr := io.Copy(io.MultiWriter(f, pr), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(partPath)
		return fmt.Errorf("下载镜像数据失败: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(partPath)
		return fmt.Errorf("写入临时下载文件失败: %w", closeErr)
	}
	if err := os.Rename(partPath, target); err != nil {
		_ = os.Remove(partPath)
		return fmt.Errorf("落盘下载文件失败: %w", err)
	}
	return nil
}

// downloadProgressWriter 统计已下载字节并按百分比上报进度（进度值变化才写 DB 限频）。
type downloadProgressWriter struct {
	ctx   *ExecContext
	total int64 // 响应 Content-Length；未知为 -1
	n     int64
	last  int
}

// Write 实现 io.Writer（挂在 MultiWriter 上，永不返回错误，避免计 progress 中断下载）。
func (w *downloadProgressWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	pct := downloadProgressPct(w.n, w.total)
	if pct != w.last {
		w.last = pct
		reportProgress(w.ctx, pct, "正在下载镜像")
	}
	return len(p), nil
}

// downloadProgressPct 已下载字节映射到任务进度（0-95；总长未知时每 16MB 推进一档）。纯函数供单测。
func downloadProgressPct(n, total int64) int {
	pct := 0
	if total > 0 {
		pct = int(n * imageProgressCeil / total)
	} else {
		pct = 2 + int(n/(16<<20))
	}
	if pct > imageProgressCeil {
		pct = imageProgressCeil
	}
	if pct < 0 {
		pct = 0
	}
	return pct
}

// registerImageRecord 下载（或跳过）完成后登记 images 表，复用 handler.RegisterImage 的登记语义：
// 同路径查询——软删记录恢复登记、活跃记录复用并刷新实测大小（不重复建行）、否则 Create。
func registerImageRecord(ctx *ExecContext, target, displayName, pool string, skipped bool) error {
	st, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("读取下载文件信息失败: %w", err)
	}
	sizeGB := float64(st.Size()) / (1024.0 * 1024.0 * 1024.0)
	fileName := filepath.Base(target)
	imgName := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	osVer := deriveOSVersion(fileName)

	var img model.Image
	err = ctx.DB.Unscoped().Where("path = ?", target).First(&img).Error
	switch {
	case err == nil && img.DeletedAt.Valid:
		// 软删记录：重新下载视为恢复（与 RegisterImage「重新登记即恢复」一致）
		if uerr := ctx.DB.Unscoped().Model(&img).Update("deleted_at", nil).Error; uerr != nil {
			return fmt.Errorf("恢复镜像登记失败: %w", uerr)
		}
	case err == nil:
		// 已登记：刷新实测大小后复用原记录
		if uerr := ctx.DB.Model(&img).Update("size_gb", sizeGB).Error; uerr != nil {
			return fmt.Errorf("刷新镜像记录失败: %w", uerr)
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
		img = model.Image{
			Name:        imgName,
			Path:        target,
			OSVersion:   osVer,
			SizeGB:      sizeGB,
			Format:      "qcow2",
			Description: fmt.Sprintf("云镜像市场下载（%s，存储池 %s）", displayName, pool),
		}
		if cerr := ctx.DB.Create(&img).Error; cerr != nil {
			return fmt.Errorf("写入镜像记录失败: %w", cerr)
		}
	default:
		return fmt.Errorf("查询镜像记录失败: %w", err)
	}

	setTaskResultImage(ctx, map[string]interface{}{
		"path":       target,
		"size":       st.Size(),
		"size_gb":    sizeGB,
		"pool":       pool,
		"image_id":   img.ID,
		"image_name": imgName,
		"os_version": osVer,
		"skipped":    skipped,
	})
	return nil
}

// setTaskResultImage 以 setTaskResultVM 的风格回填镜像类任务结果
// （无 VM 关联，不动 VMID/VMName，避免把镜像任务误标到某台虚拟机上）。
func setTaskResultImage(ctx *ExecContext, result map[string]interface{}) {
	if b, err := json.Marshal(result); err == nil {
		ctx.Task.Result = string(b)
	}
}

// deriveOSVersion 从镜像文件名关键词推导 OS 展示名（值须与 virt.OSList 的 Name 精确一致——
// 创建向导「基于云镜像创建」按 images.os_version 自动选中 OS；推导不出返回空串由用户手选）。
// 纯函数供单测；OSList 条目变更时同步本函数。
func deriveOSVersion(fileName string) string {
	s := strings.ToLower(fileName)
	switch {
	case strings.Contains(s, "ubuntu-24.04"):
		return "Ubuntu 24.04 LTS"
	case strings.Contains(s, "ubuntu-22.04"):
		return "Ubuntu 22.04 LTS"
	case strings.Contains(s, "ubuntu-20.04"):
		return "Ubuntu 20.04 LTS"
	case strings.Contains(s, "rocky-9"), strings.Contains(s, "rocky9"):
		return "Rocky Linux 9"
	case strings.Contains(s, "rocky-8"), strings.Contains(s, "rocky8"):
		return "Rocky Linux 8"
	case strings.Contains(s, "debian-12"), strings.Contains(s, "bookworm"):
		return "Debian 12"
	case strings.Contains(s, "debian-11"), strings.Contains(s, "bullseye"):
		return "Debian 11"
	case strings.Contains(s, "almalinux-9"), strings.Contains(s, "almalinux9"):
		// virt.OSList 暂无 AlmaLinux 条目，该值入库后自动识别会落空（OSList 补条目即闭环）
		return "AlmaLinux 9"
	case strings.Contains(s, "fedora"):
		// OSList 的 Fedora 粒度只有 "Fedora 40"（virtio 设备模型跨版本一致），
		// 新版 Fedora 云镜像（如 44）统一推导到该最近项
		return "Fedora 40"
	default:
		return ""
	}
}
