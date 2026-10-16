package handler

// image.go 镜像库核心：处理器装配、镜像库/安装介质存储池约定、镜像列表（含所属池标注）
// 与安全文件名工具。上传/登记/删除见 image_manage.go，模板标记与从镜像建机见 image_clone.go。

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// ImageHandler 镜像处理器
type ImageHandler struct {
	DB    *gorm.DB
	Virt  *virt.Virt
	Tasks *tasks.Manager
}

// NewImageHandler 创建镜像处理器
func NewImageHandler(db *gorm.DB, taskMgr *tasks.Manager) *ImageHandler {
	return &ImageHandler{DB: db, Virt: virt.New(), Tasks: taskMgr}
}

// imagePool 镜像库存储池名：上传的磁盘镜像与镜像市场 qcow2 落地于此，与模板基盘同池（base）。
// 落在该池目录下即被 libvirt 目录池扫描识别，与手动 virsh vol-create 同口径。
const imagePool = "base"

// isoPool 安装介质存储池名：官方安装 ISO 落此池（与磁盘镜像库分离，存储页按池角色标注）。
const isoPool = "iso"

// ensureImagePool 确保镜像池存在：不存在时按 ImageDir 自动建目录池（镜像统一存 base 池）。
func (h *ImageHandler) ensureImagePool() (string, error) {
	pools, err := h.Virt.ListPools()
	if err != nil {
		return "", fmt.Errorf("获取存储池列表失败: %w", err)
	}
	found := false
	for _, p := range pools {
		if p == imagePool {
			found = true
			break
		}
	}
	if !found {
		imageDir := config.GlobalConfig.ImageDir
		if imageDir == "" {
			imageDir = "/var/lib/libvirt/images"
		}
		if err := h.Virt.CreateDirPool(imagePool, imageDir); err != nil {
			return "", fmt.Errorf("自动创建镜像池 %s 失败: %w", imagePool, err)
		}
	}
	return h.Virt.GetPoolPath(imagePool)
}

// ListImages 获取镜像列表。每个 item 附 pool 字段：镜像文件所属存储池名。
// 归属规则：池路径（virsh pool-dumpxml 的 target/path）按目录前缀匹配 image.path，
// 多个命中取最长前缀（防嵌套目录池误配），路径边界须落在目录分隔符上（/a 与 /a/b 两个池，/abc 不算命中）；
// 匹配不到置空串，前端显示「—」。池枚举失败不阻断列表主流程（列表以 DB 为准，pool 只是展示性标注）。
func (h *ImageHandler) ListImages(c *gin.Context) {
	var images []model.Image
	query := h.DB.Order("created_at desc")

	if c.Query("is_template") != "" {
		isTpl := c.Query("is_template") == "true" || c.Query("is_template") == "1"
		query = query.Where("is_template = ?", isTpl)
	}

	if err := query.Find(&images).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "查询镜像失败")
		return
	}

	// 池路径 → 池名映射：ListPools（virsh pool-list --all）+ GetPoolPath（virsh pool-dumpxml 取 target/path）
	type poolPrefix struct {
		name string
		path string
	}
	prefixes := make([]poolPrefix, 0, 8)
	pools, err := h.Virt.ListPools()
	if err != nil {
		// 归属标注失败不影响列表本身：完整错误进日志，pool 全部置空
		LogError(c, fmt.Errorf("镜像归属存储池标注失败（枚举存储池）: %w", err))
	}
	for _, p := range pools {
		if path, err := h.Virt.GetPoolPath(p); err == nil && path != "" {
			prefixes = append(prefixes, poolPrefix{name: p, path: path})
		}
	}
	// 长路径优先：嵌套目录池（/a 与 /a/b）时短前缀会抢先命中，先比长前缀
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i].path) > len(prefixes[j].path) })

	sep := string(filepath.Separator)
	items := make([]gin.H, 0, len(images))
	for _, img := range images {
		pool := ""
		for _, pp := range prefixes {
			if img.Path == pp.path || strings.HasPrefix(img.Path, pp.path+sep) {
				pool = pp.name
				break
			}
		}
		items = append(items, gin.H{
			"id":          img.ID,
			"name":        img.Name,
			"path":        img.Path,
			"os_version":  img.OSVersion,
			"size_gb":     img.SizeGB,
			"format":      img.Format,
			"is_template": img.IsTemplate,
			"description": img.Description,
			"created_at":  img.CreatedAt,
			"updated_at":  img.UpdatedAt,
			"pool":        pool,
		})
	}

	Success(c, gin.H{
		"total": len(images),
		"items": items,
	})
}

// sanitizeFileName 仅保留安全字符，过滤路径分隔符与特殊字符
func sanitizeFileName(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
