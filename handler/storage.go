package handler

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/setting"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// volNameRegex 卷/池名称允许字母数字下划线连字符和点（卷名含扩展名）
var volNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// poolPathRegex 存储池宿主机路径字符白名单：以 / 开头，只允许字母、数字、下划线、连字符、点和斜杠。
// 排除引号、尖括号等 XML 元字符，与 virt 层的 encoding/xml 序列化构成纵深防御。
var poolPathRegex = regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`)

// validVolName 校验卷/池名称合法性
func validVolName(name string) bool {
	return volNameRegex.MatchString(name)
}

// validPoolPath 校验目录型存储池的宿主机路径（对应 virsh pool-define-as --target）。
// 目录池的 path 决定该池所有卷的落盘位置，零校验等于让调用方指定宿主机任意目录，// 因此要求：绝对路径 + 字符白名单 + 规范写法（filepath.Clean 后与原值一致，
// 借此拒绝 ..、// 与结尾斜杠）+ 不得为根目录本身。
func validPoolPath(p string) bool {
	if !filepath.IsAbs(p) || !poolPathRegex.MatchString(p) {
		return false
	}
	clean := filepath.Clean(p)
	if clean != p || clean == "/" || strings.Contains(clean, "..") {
		return false
	}
	return true
}

// validVolFormat 校验卷格式白名单：只允许 qcow2 / raw（与前端下拉选项一致）。
// 空值保持原行为不变（由 virt 层 CreateVolumeCustom 默认 qcow2）。
func validVolFormat(format string) bool {
	switch format {
	case "", "qcow2", "raw":
		return true
	}
	return false
}

// VolumeRefs 单个存储卷的在用引用信息（删卷守卫与"在用"徽标共用一份数据）。
type VolumeRefs struct {
	VMs      []string `json:"vms"`      // 直接挂载该卷的虚拟机名
	Images   []string `json:"images"`   // 镜像库中登记该路径的镜像名
	Children []string `json:"children"` // 以该卷为 backing 父盘的子卷名（增量克隆）
}

// inUse 该卷是否仍被任何资源引用。
func (r *VolumeRefs) inUse() bool {
	return len(r.VMs) > 0 || len(r.Images) > 0 || len(r.Children) > 0
}

// StorageHandler 存储池处理器
type StorageHandler struct {
	DB    *gorm.DB
	Virt  *virt.Virt
	Tasks *tasks.Manager
}

// NewStorageHandler 创建存储池处理器
func NewStorageHandler(db *gorm.DB, taskMgr *tasks.Manager) *StorageHandler {
	return &StorageHandler{DB: db, Virt: virt.New(), Tasks: taskMgr}
}

// poolRoleNames 平台认池角色的全集（pool_meta.role 白名单；空串 = 跟随自动推断）。
var poolRoleNames = []string{"模板基盘", "系统盘", "数据盘", "安装镜像", "系统池", "其他"}

// inferPoolRole 按池名/路径推断池角色（纯函数，供列表展示与单测）。
// libvirt 池 XML 没有语义字段，这里按本平台的目录约定给缺省角色：
// base=模板基盘（backing 父盘与模板）、images=系统盘（增量克隆子卷）、
// exten=数据盘（热挂数据盘）、img=安装镜像（ISO）、default=/var/lib=系统池。
func inferPoolRole(name, path string) string {
	switch {
	case name == "base" || strings.HasSuffix(path, "/storage/base"):
		return "模板基盘"
	case name == "images" || strings.HasSuffix(path, "/storage/images"):
		return "系统盘"
	case name == "exten" || strings.HasSuffix(path, "/storage/exten"):
		return "数据盘"
	case name == "img" || strings.Contains(path, "/data/img"):
		return "安装镜像"
	case strings.HasPrefix(path, "/var/lib/libvirt"):
		return "系统池"
	default:
		return ""
	}
}

// loadPoolMeta 读取池元数据（角色覆盖 + 描述），key 为池名。
func (h *StorageHandler) loadPoolMeta() map[string]model.PoolMeta {
	metas := map[string]model.PoolMeta{}
	var rows []model.PoolMeta
	if err := h.DB.Find(&rows).Error; err != nil {
		return metas
	}
	for _, r := range rows {
		metas[r.PoolName] = r
	}
	return metas
}

// seedDirInfo cloud-init 种子目录信息（路径 + seed 文件数）：seed 是每 VM 一份的生成物，
// 不进池管理，在存储页汇总条展示。
func (h *StorageHandler) seedDirInfo() gin.H {
	dir := config.GlobalConfig.SeedDir
	if dir == "" {
		dir = "/home/jiuzhao/vmops/data/seed"
	}
	count := 0
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), "-seed.iso") {
				count++
			}
		}
	}
	return gin.H{"path": dir, "count": count}
}

// defaultPoolName 平台默认存储池（建机未指定池时落点），读系统设置，缺省走 setting 兜底值。
func (h *StorageHandler) defaultPoolName() string {
	var row model.Setting
	if err := h.DB.Where("`key` = ?", "default_storage_pool").First(&row).Error; err == nil && row.Value != "" {
		return row.Value
	}
	return setting.DefaultStoragePoolFallback
}

// UpdatePoolMeta 更新池的平台侧元数据（PUT /api/storage/pools/:name/meta）。
// role 必须在白名单内或空串（空 = 跟随自动推断）；description 最长 500 字符。
func (h *StorageHandler) UpdatePoolMeta(c *gin.Context) {
	name := c.Param("name")
	if !validVolName(name) {
		Fail(c, http.StatusBadRequest, "存储池名称不合法")
		return
	}
	var req struct {
		Role        string `json:"role"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	validRole := req.Role == ""
	for _, r := range poolRoleNames {
		if req.Role == r {
			validRole = true
		}
	}
	if !validRole {
		Fail(c, http.StatusBadRequest, "角色只允许："+strings.Join(poolRoleNames, "/")+"或留空自动推断")
		return
	}
	if len(req.Description) > 500 {
		Fail(c, http.StatusBadRequest, "描述最长 500 字符")
		return
	}

	var meta model.PoolMeta
	err := h.DB.Where("pool_name = ?", name).First(&meta).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			meta = model.PoolMeta{PoolName: name}
		} else {
			ErrorWithMessage(c, http.StatusInternalServerError, "查询池元数据失败", err)
			return
		}
	}
	meta.Role = req.Role
	meta.Description = req.Description
	if err := h.DB.Save(&meta).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "保存池元数据失败", err)
		return
	}
	Success(c, meta)
}

// ListPools 存储池列表（含详情与平台侧角色/描述）
func (h *StorageHandler) ListPools(c *gin.Context) {
	pools, err := h.Virt.ListPoolInfos()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取存储池失败", err)
		return
	}

	// 平台侧元数据：角色（DB 覆盖优先，缺省按名/路径推断）与描述（libvirt 池无此语义，只能平台存）
	metas := h.loadPoolMeta()
	items := make([]gin.H, 0, len(pools))
	for _, p := range pools {
		role := inferPoolRole(p.Name, p.Path)
		description := ""
		if m, ok := metas[p.Name]; ok {
			if m.Role != "" {
				role = m.Role
			}
			description = m.Description
		}
		items = append(items, gin.H{
			"name":        p.Name,
			"active":      p.Active,
			"persistent":  p.Persistent,
			"path":        p.Path,
			"capacity":    p.Capacity,
			"allocation":  p.Allocation,
			"available":   p.Available,
			"vol_count":   p.VolCount,
			"role":        role,
			"description": description,
		})
	}

	Success(c, gin.H{
		"total":        len(pools),
		"items":        items,
		"seed_dir":     h.seedDirInfo(),
		"default_pool": h.defaultPoolName(),
		"pool_roles":   poolRoleNames,
	})
}

// GetPool 存储池详情（含卷列表）
func (h *StorageHandler) GetPool(c *gin.Context) {
	name := c.Param("name")
	info, err := h.Virt.GetPoolInfo(name)
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, err)
		return
	}

	Success(c, info)
}

// CreatePool 创建目录型存储池
func (h *StorageHandler) CreatePool(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
		Path string `json:"path" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	if !validVolName(req.Name) {
		Fail(c, http.StatusBadRequest, "存储池名称只允许字母、数字、下划线、连字符和点")
		return
	}
	if !validPoolPath(req.Path) {
		Fail(c, http.StatusBadRequest, "存储池路径必须是规范的绝对路径（不含 ..、结尾斜杠），且不能是根目录")
		return
	}

	if err := h.Virt.CreateDirPool(req.Name, req.Path); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": req.Name, "path": req.Path})
}

// DeletePool 删除存储池
func (h *StorageHandler) DeletePool(c *gin.Context) {
	name := c.Param("name")
	if err := h.Virt.DeleteDirPool(name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": name})
}

// CreateVolume 在存储池创建卷
func (h *StorageHandler) CreateVolume(c *gin.Context) {
	poolName := c.Param("name")
	var req struct {
		Name     string `json:"name" binding:"required"`
		Format   string `json:"format"`
		Capacity int    `json:"capacity"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !validVolName(req.Name) {
		Fail(c, http.StatusBadRequest, "卷名称只允许字母、数字、下划线、连字符和点")
		return
	}
	if !validVolFormat(req.Format) {
		Fail(c, http.StatusBadRequest, "卷格式只支持 qcow2 或 raw")
		return
	}
	if req.Capacity <= 0 {
		req.Capacity = 20
	}

	if err := h.Virt.CreateVolumeCustom(poolName, req.Name, req.Format, req.Capacity); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"pool": poolName, "name": req.Name})
}

// poolVolumeRefs 计算存储池内每个卷的在用引用：虚拟机挂载 / 镜像库登记 / 增量克隆子卷。
// 池级一次算全，供引用查询端点与删卷守卫共用（对应 virsh vol-dumpxml + domblklist + vol-list）。
func (h *StorageHandler) poolVolumeRefs(poolName string) (map[string]*VolumeRefs, error) {
	pool, err := h.Virt.GetPoolInfo(poolName)
	if err != nil {
		return nil, err
	}
	// 父盘路径 → 子卷名列表（增量克隆 backing 链）；失败按空处理，不让守卫因枚举失败而失效
	backing, _ := h.Virt.ListBackingRefs(poolName)
	disks, _ := h.Virt.ListAllDomainDiskSources()

	refs := make(map[string]*VolumeRefs, len(pool.Volumes))
	for _, vol := range pool.Volumes {
		r := &VolumeRefs{}
		// 虚拟机挂载：域磁盘 source 精确命中卷路径
		for domName, paths := range disks {
			for _, p := range paths {
				if p == vol.Path {
					r.VMs = append(r.VMs, domName)
					break
				}
			}
		}
		// 镜像库登记：images.path 保存的是落盘绝对路径，精确匹配
		if h.DB != nil {
			var images []model.Image
			if err := h.DB.Where("path = ?", vol.Path).Find(&images).Error; err == nil {
				for _, im := range images {
					r.Images = append(r.Images, im.Name)
				}
			}
		}
		// 增量克隆子卷
		r.Children = backing[vol.Path]
		refs[vol.Name] = r
	}
	return refs, nil
}

// GetVolumeRefs 查询存储池内所有卷的在用引用（GET /api/storage/pools/:name/volume-refs）。
// 前端卷管理弹窗据此显示"在用"徽标，删卷确认文案带上引用详情。
func (h *StorageHandler) GetVolumeRefs(c *gin.Context) {
	poolName := c.Param("name")
	refs, err := h.poolVolumeRefs(poolName)
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, err)
		return
	}
	Success(c, gin.H{"pool": poolName, "refs": refs})
}

// DeleteVolume 删除存储池中的卷。
// 删除前过在用守卫：仍被虚拟机挂载 / 已登记镜像库 / 是增量克隆父盘的卷一律拒绝删除，
// 与 tasks.execDeleteVM 的 shouldKeepVol 三重守卫同一立场——宁可删不掉，不可损坏在用磁盘。
func (h *StorageHandler) DeleteVolume(c *gin.Context) {
	poolName := c.Param("name")
	volName := c.Param("vol")

	refs, err := h.poolVolumeRefs(poolName)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if r, ok := refs[volName]; ok && r.inUse() {
		Fail(c, http.StatusConflict, volumeInUseReason(volName, r))
		return
	}

	if err := h.Virt.DeleteVolume(poolName, volName); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"pool": poolName, "vol": volName})
}

// CleanupOrphans 清理存储池孤儿卷（POST /api/storage/pools/:name/orphan-cleanup，转后台任务）。
// 孤儿 = 不被虚拟机挂载、未登记镜像库、也非任何子卷 backing 父盘的卷；判定由任务执行器完成，
// 前端确认弹窗先经 volume-refs 端点列出候选。任务结果含 deleted/kept 明细。
func (h *StorageHandler) CleanupOrphans(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	poolName := c.Param("name")
	if !validVolName(poolName) {
		Fail(c, http.StatusBadRequest, "存储池名称只允许字母、数字、下划线、连字符和点")
		return
	}
	userID, _ := c.Get("user_id")
	username, _ := c.Get("username")
	var uid *uint
	if v, ok := userID.(uint); ok {
		uid = &v
	}
	uname, _ := username.(string)

	task, err := h.Tasks.Submit("cleanup_volumes", "清理孤儿卷 "+poolName,
		gin.H{"pool": poolName}, uid, uname, "", nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"code":    http.StatusAccepted,
		"message": "清理任务已提交",
		"data":    gin.H{"task_id": task.ID},
	})
}

// volumeInUseReason 生成删卷守卫的中文拒绝原因（逐类列出引用方）。
func volumeInUseReason(volName string, r *VolumeRefs) string {
	var reasons []string
	if len(r.VMs) > 0 {
		reasons = append(reasons, fmt.Sprintf("仍被虚拟机挂载（%s），请先卸载磁盘或删除对应虚拟机", strings.Join(r.VMs, "、")))
	}
	if len(r.Images) > 0 {
		reasons = append(reasons, fmt.Sprintf("已登记为镜像库镜像（%s），请先在镜像管理中删除该镜像", strings.Join(r.Images, "、")))
	}
	if len(r.Children) > 0 {
		reasons = append(reasons, fmt.Sprintf("是增量克隆父盘，仍被 %d 个子卷依赖（%s）", len(r.Children), strings.Join(r.Children, "、")))
	}
	return fmt.Sprintf("卷 %s 正在使用中，已阻止删除: %s", volName, strings.Join(reasons, "；"))
}
