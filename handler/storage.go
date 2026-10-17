package handler

// storage.go 存储池与卷的基础管理：处理器装配、池列表/详情、目录型池 CRUD 与卷创建。
// 名称/路径/格式校验见 storage_validate.go，池角色推断与平台侧元数据见 storage_meta.go。
// 卷的在用引用与删除守卫见 storage_refs.go，克隆家谱见 storage_graph.go。

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

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
		description := inferPoolDesc(p.Name, p.Path) // 缺省文案内置，用户不必为此编辑
		if m, ok := metas[p.Name]; ok {
			if m.Role != "" {
				role = m.Role
			}
			if strings.TrimSpace(m.Description) != "" {
				description = m.Description // 显式设过才覆盖缺省
			}
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
