package handler

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// ImageHandler 镜像处理器
type ImageHandler struct {
	DB *gorm.DB
}

// NewImageHandler 创建镜像处理器
func NewImageHandler(db *gorm.DB) *ImageHandler {
	return &ImageHandler{DB: db}
}

// ListImages 获取镜像列表
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

	Success(c, gin.H{
		"total": len(images),
		"items": images,
	})
}

// GetImage 获取镜像详情
func (h *ImageHandler) GetImage(c *gin.Context) {
	id := c.Param("id")

	var img model.Image
	if err := h.DB.First(&img, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "镜像不存在")
		return
	}

	Success(c, img)
}

// UploadImage 上传镜像
func (h *ImageHandler) UploadImage(c *gin.Context) {
	name := c.PostForm("name")
	if name == "" {
		Fail(c, http.StatusBadRequest, "镜像名称不能为空")
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		Fail(c, http.StatusBadRequest, "未找到上传文件")
		return
	}

	osVersion := c.PostForm("os_version")
	isTemplate := c.PostForm("is_template") == "true" || c.PostForm("is_template") == "1"

	// 确保存储目录存在
	imageDir := config.GlobalConfig.ImageDir
	if imageDir == "" {
		imageDir = "/var/lib/libvirt/images"
	}
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		Fail(c, http.StatusInternalServerError, "创建镜像目录失败")
		return
	}

	// 安全文件名：仅保留基名并清洗，避免目录穿越
	base := sanitizeFileName(filepath.Base(file.Filename))
	if base == "" {
		base = sanitizeFileName(name) + ".qcow2"
	}
	ext := strings.ToLower(filepath.Ext(base))
	stem := strings.TrimSuffix(base, ext)
	storedName := fmt.Sprintf("%s_%d%s", stem, time.Now().UnixNano(), ext)
	dst := filepath.Join(imageDir, storedName)

	// 流式写入目标文件
	src, err := file.Open()
	if err != nil {
		Fail(c, http.StatusInternalServerError, "打开上传文件失败")
		return
	}
	defer src.Close()

	out, err := os.Create(dst)
	if err != nil {
		Fail(c, http.StatusInternalServerError, "创建目标文件失败")
		return
	}
	defer out.Close()

	written, err := io.Copy(out, src)
	if err != nil {
		os.Remove(dst)
		Fail(c, http.StatusInternalServerError, "写入文件失败")
		return
	}

	// 计算大小(GB)与格式
	sizeGB := float64(written) / (1024.0 * 1024.0 * 1024.0)
	format := "qcow2"
	switch ext {
	case ".raw":
		format = "raw"
	case ".vmdk":
		format = "vmdk"
	case ".qcow2":
		format = "qcow2"
	}

	img := model.Image{
		Name:       name,
		Path:       dst,
		OSVersion:  osVersion,
		SizeGB:     sizeGB,
		Format:     format,
		IsTemplate: isTemplate,
	}
	if err := h.DB.Create(&img).Error; err != nil {
		os.Remove(dst)
		Fail(c, http.StatusInternalServerError, "保存镜像记录失败")
		return
	}

	Created(c, "上传成功", img)
}

// DeleteImage 删除镜像
func (h *ImageHandler) DeleteImage(c *gin.Context) {
	id := c.Param("id")

	var img model.Image
	if err := h.DB.First(&img, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "镜像不存在")
		return
	}

	// 软删除数据库记录
	if err := h.DB.Delete(&img).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "删除镜像记录失败")
		return
	}

	// 删除磁盘文件（仅当位于镜像目录下，防止误删系统文件）
	imageDir := config.GlobalConfig.ImageDir
	if imageDir == "" {
		imageDir = "/var/lib/libvirt/images"
	}
	if img.Path != "" && strings.HasPrefix(img.Path, imageDir) {
		if err := os.Remove(img.Path); err != nil && !os.IsNotExist(err) {
			// 文件删除失败不阻断主流程，记录后继续
			c.Error(err)
		}
	}

	Success(c, gin.H{"message": "镜像已删除"})
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
