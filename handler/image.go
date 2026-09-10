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
	id, ok := paramID(c, "id")
	if !ok {
		return
	}

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

// RegisterImage 登记既有存储卷为平台云镜像（POST /api/images/register）。
// 用于把 base 等池里已存在的模板/云镜像纳入镜像库，打通「基于云镜像创建」链路——
// 这些文件是 VM 正在引用或将被 backing 的共享盘，登记只是建目录索引，不复制不移动。
// body: {name*, path*, os_version?, description?, is_template?}；同路径重复登记返回 409。
func (h *ImageHandler) RegisterImage(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		Path        string `json:"path" binding:"required"`
		OSVersion   string `json:"os_version"`
		Description string `json:"description"`
		IsTemplate  bool   `json:"is_template"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	// path 必须是存在的常规文件：登记的是磁盘索引，指向不存在文件的记录只会制造悬挂引用
	if !filepath.IsAbs(req.Path) || !poolPathRegex.MatchString(req.Path) {
		Fail(c, http.StatusBadRequest, "路径必须是绝对路径且只含合法字符")
		return
	}
	st, err := os.Stat(req.Path)
	if err != nil || st.IsDir() {
		Fail(c, http.StatusBadRequest, "文件不存在或不是常规文件："+req.Path)
		return
	}

	// 同路径去重（含软删除记录——重新登记视为恢复，复用原记录）
	var existing model.Image
	if err := h.DB.Unscoped().Where("path = ?", req.Path).First(&existing).Error; err == nil {
		if existing.DeletedAt.Valid {
			if err := h.DB.Unscoped().Model(&existing).Update("deleted_at", nil).Error; err != nil {
				ErrorWithMessage(c, http.StatusInternalServerError, "恢复镜像登记失败", err)
				return
			}
			Created(c, "已恢复登记", existing)
			return
		}
		Fail(c, http.StatusConflict, "该路径已登记为镜像「"+existing.Name+"」")
		return
	}

	ext := strings.ToLower(filepath.Ext(req.Path))
	format := "qcow2"
	switch ext {
	case ".iso":
		format = "iso"
	case ".raw":
		format = "raw"
	}
	sizeGB := float64(st.Size()) / (1024.0 * 1024.0 * 1024.0)

	img := model.Image{
		Name:        req.Name,
		Path:        req.Path,
		OSVersion:   req.OSVersion,
		SizeGB:      sizeGB,
		Format:      format,
		IsTemplate:  req.IsTemplate,
		Description: req.Description,
	}
	if err := h.DB.Create(&img).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "保存镜像记录失败", err)
		return
	}
	Created(c, "登记成功", img)
}

// DeleteImage 删除镜像
func (h *ImageHandler) DeleteImage(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}

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
	// 删除前必须过引用守卫：该镜像可能正被 VM 以 source_image_id 直接引用（不拷贝），
	// 或是其他卷的 backing 父盘——os.Remove 绕过 libvirt，没有任何报错兜底，删了就是不可逆损坏。
	// 与 storage.DeleteVolume / shouldKeepVol 同一立场：宁可删不掉，不可损坏在用磁盘。
	poolPath := ""
	if p, err := h.Virt.GetPoolPath(imagePool); err == nil {
		poolPath = p
	}
	if img.Path != "" {
		inPool := poolPath != "" && strings.HasPrefix(img.Path, poolPath)
		inDir := strings.HasPrefix(img.Path, config.GlobalConfig.ImageDir)
		if inPool || inDir {
			if refs, refErr := h.imageRefs(img.Path); refErr == nil && refs != "" {
				Fail(c, http.StatusConflict, "镜像正被使用，拒绝删除："+refs)
				return
			}
			if err := os.Remove(img.Path); err != nil && !os.IsNotExist(err) {
				// 文件删除失败不阻断主流程，记录后继续
				c.Error(err)
			}
		}
	}

	Success(c, gin.H{"message": "镜像已删除"})
}

// imageRefs 计算文件路径的在用引用：被 VM 挂载 / 被任何池内卷当 backing 父盘。
// 返回空串 = 无引用。查不到的部分静默跳过（与删除守卫的保守取向一致——查不到引用时放行删除，
// 但虚拟机侧挂载查询失败会记日志，避免完全盲删）。
func (h *ImageHandler) imageRefs(path string) (string, error) {
	var refs []string
	sources, err := h.Virt.ListAllDomainDiskSources()
	if err != nil {
		return "", err
	}
	for dom, paths := range sources {
		for _, p := range paths {
			if p == path {
				refs = append(refs, "被虚拟机 "+dom+" 挂载")
			}
		}
	}
	// backing 依赖：扫描常见池（base/images/exten），父盘路径命中即视为在用
	for _, pool := range []string{"base", "images", "exten", imagePool} {
		backing, err := h.Virt.ListBackingRefs(pool)
		if err != nil {
			continue
		}
		if children, ok := backing[path]; ok && len(children) > 0 {
			refs = append(refs, fmt.Sprintf("被 %d 个卷当 backing 父盘", len(children)))
			break
		}
	}
	return strings.Join(refs, "、"), nil
}

// SetImageTemplate 标记/取消镜像为模板（body: {is_template}）。
func (h *ImageHandler) SetImageTemplate(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
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

// CloneVM 基于镜像/模板创建虚拟机（异步：校验后 Submit clone_image_vm，后台执行 linked clone）。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
func (h *ImageHandler) CloneVM(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
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
	payload := map[string]interface{}{
		"image_id":     img.ID,
		"name":         req.Name,
		"storage_pool": req.StoragePool,
		"vcpu":         req.VCPU,
		"memory_mb":    req.MemoryMB,
		"network":      req.Network,
	}
	if req.CloudInit != nil {
		payload["cloud_init"] = req.CloudInit
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("clone_image_vm", "从镜像创建虚拟机 "+req.Name, payload, userID, username, req.Name, nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"code":    http.StatusAccepted,
		"message": "任务已提交",
		"data":    gin.H{"task_id": task.ID},
	})
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
