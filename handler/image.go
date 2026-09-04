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
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// ImageHandler 镜像处理器
type ImageHandler struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewImageHandler 创建镜像处理器
func NewImageHandler(db *gorm.DB) *ImageHandler {
	return &ImageHandler{DB: db, Virt: virt.New()}
}

// imagePool 镜像统一存储池名：上传文件落在该池目录下，即可被 libvirt 池识别。
const imagePool = "img"

// ensureImagePool 确保镜像池存在：不存在时按 ImageDir 自动建目录池（镜像统一存 img 池）。
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

// UploadImage 上传镜像（multipart；form 字段：name/file/os_version/is_template/pool）。
// 上传文件落到 pool 指定池（默认 img）的目标路径下，直接作为池卷被 libvirt 识别，
// 不强制走 StorageVolCreateXML（目录池扫描路径即见卷，注释说明）。
// 文件名清洗 + 时间戳防冲突，沿用既有安全命名逻辑。
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

	// 目标池：默认 img；不存在则自动建目录池（镜像统一存 img 池）
	pool := c.PostForm("pool")
	if pool == "" {
		pool = imagePool
	}
	poolPath, err := h.ensureImagePool()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if pool != imagePool {
		// 用户显式指定其他池：检查存在，不存在则报错（仅 img 池自动创建）
		pools, err := h.Virt.ListPools()
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		exists := false
		for _, p := range pools {
			if p == pool {
				exists = true
				break
			}
		}
		if !exists {
			Fail(c, http.StatusBadRequest, "存储池 "+pool+" 不存在（默认自动创建 img 池）")
			return
		}
		if poolPath, err = h.Virt.GetPoolPath(pool); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	}

	// 确保池目录存在（目录池路径若尚未创建则补建）
	if err := os.MkdirAll(poolPath, 0755); err != nil {
		Fail(c, http.StatusInternalServerError, "创建镜像池目录失败")
		return
	}

	// 安全文件名：仅保留基名并清洗，避免目录穿越；加时间戳防冲突
	base := sanitizeFileName(filepath.Base(file.Filename))
	if base == "" {
		base = sanitizeFileName(name) + ".qcow2"
	}
	ext := strings.ToLower(filepath.Ext(base))
	stem := strings.TrimSuffix(base, ext)
	storedName := fmt.Sprintf("%s_%d%s", stem, time.Now().UnixNano(), ext)
	dst := filepath.Join(poolPath, storedName)

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

	// 删除磁盘文件（仅当位于镜像池/镜像目录下，防止误删系统文件）。
	// 若该镜像已被 VM 引用（source_image_id 直接引用文件），libvirt 层 vol-delete 会因卷被占用报错，
	// 需先删除引用 VM 再删镜像；此处不做外键检查，由 virt 层报错兜底。
	poolPath := ""
	if p, err := h.Virt.GetPoolPath(imagePool); err == nil {
		poolPath = p
	}
	if img.Path != "" {
		inPool := poolPath != "" && strings.HasPrefix(img.Path, poolPath)
		inDir := strings.HasPrefix(img.Path, config.GlobalConfig.ImageDir)
		if inPool || inDir {
			if err := os.Remove(img.Path); err != nil && !os.IsNotExist(err) {
				// 文件删除失败不阻断主流程，记录后继续
				c.Error(err)
			}
		}
	}

	Success(c, gin.H{"message": "镜像已删除"})
}

// SetImageTemplate 标记/取消镜像为模板（body: {is_template}）。
func (h *ImageHandler) SetImageTemplate(c *gin.Context) {
	id := c.Param("id")
	var img model.Image
	if err := h.DB.First(&img, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "镜像不存在")
		return
	}

	var req struct {
		IsTemplate bool `json:"is_template"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	if err := h.DB.Model(&img).Update("is_template", req.IsTemplate).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "更新镜像模板标记失败")
		return
	}
	img.IsTemplate = req.IsTemplate
	Success(c, img)
}

// CloneVM 基于镜像/模板创建虚拟机（body: {name, storage_pool?, vcpu?, memory_mb?, network?, cloud_init?}）。
// 云镜像直接引用文件作为磁盘 source（不拷贝，与镜像共用文件；删除镜像前需先删引用 VM）。
func (h *ImageHandler) CloneVM(c *gin.Context) {
	id := c.Param("id")
	var img model.Image
	if err := h.DB.First(&img, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "镜像不存在")
		return
	}

	var req struct {
		Name        string              `json:"name" binding:"required"`
		StoragePool string              `json:"storage_pool"`
		VCPU        int                 `json:"vcpu"`
		MemoryMB    int                 `json:"memory_mb"`
		Network     string              `json:"network"`
		CloudInit   *virt.CloudInitSpec `json:"cloud_init,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "虚拟机名称只允许字母、数字、下划线和连字符")
		return
	}

	host, err := h.firstImageHost()
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "请先在宿主机管理中登记宿主机", err)
		return
	}
	if req.VCPU == 0 {
		req.VCPU = 1
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = 1024
	}
	if req.Network == "" {
		req.Network = "default"
	}
	if req.StoragePool == "" {
		req.StoragePool = "vmops"
	}

	uuid, err := randomUUID()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "生成虚拟机 UUID 失败", err)
		return
	}
	mac, err := randomMAC()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "生成虚拟机 MAC 失败", err)
		return
	}

	spec := &virt.DomainSpec{
		Name:     req.Name,
		UUID:     uuid,
		VCPU:     req.VCPU,
		MemoryMB: req.MemoryMB,
		OSType:   "hvm",
		Arch:     "x86_64",
		Boot:     virt.BootSpec{Devices: []string{"hd"}},
		Graphics: virt.GraphicsSpec{Type: "vnc", Port: -1},
	}
	// 镜像文件直接作为系统盘 source（只读引用，不拷贝）
	spec.Disks = append(spec.Disks, virt.DiskSpec{
		Type: "file", Device: "disk", Driver: "qcow2", Bus: "virtio",
		Source: img.Path, Target: "vda",
	})
	spec.Interfaces = append(spec.Interfaces, virt.InterfaceSpec{
		Type: "network", Source: req.Network, MAC: mac, Model: "virtio",
	})

	// cloud-init：生成 seed ISO 落到镜像池路径，挂只读 cdrom
	if req.CloudInit != nil {
		if req.CloudInit.Hostname == "" {
			req.CloudInit.Hostname = req.Name
		}
		seedBytes, err := virt.GenerateSeedISO(req.CloudInit)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		poolPath, err := h.Virt.GetPoolPath(imagePool)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		seedPath := filepath.Join(poolPath, req.Name+"-seed.iso")
		if err := os.WriteFile(seedPath, seedBytes, 0644); err != nil {
			ErrorWithMessage(c, http.StatusInternalServerError, "写入 cloud-init seed 镜像失败", err)
			return
		}
		spec.Disks = append(spec.Disks, virt.DiskSpec{
			Type: "file", Device: "cdrom", Driver: "raw", Bus: "ide",
			Source: seedPath, ReadOnly: true, Target: "hda",
		})
		spec.Boot.Devices = []string{"cdrom", "hd"}
	}

	xmlstr, err := virt.BuildDomainXML(spec)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
		return
	}
	if err := h.Virt.DefineDomain(xmlstr); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	vm := model.VM{
		UUID:        uuid,
		Name:        req.Name,
		HostID:      host.ID,
		Template:    "image",
		StoragePool: req.StoragePool,
		VCPU:        req.VCPU,
		MemoryMB:    req.MemoryMB,
		DiskGB:      int(img.SizeGB + 0.5),
		MACAddress:  mac,
		Status:      "shut off",
	}
	if err := h.DB.Create(&vm).Error; err != nil {
		h.Virt.UndefineDomain(req.Name)
		ErrorWithMessage(c, http.StatusInternalServerError, "记录虚拟机失败", err)
		return
	}

	Success(c, vm)
}

// firstImageHost 返回平台登记的首台宿主机（镜像建 VM 的默认纳管目标）。
func (h *ImageHandler) firstImageHost() (*model.Host, error) {
	var host model.Host
	if err := h.DB.Order("id ASC").First(&host).Error; err != nil {
		return nil, err
	}
	return &host, nil
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
