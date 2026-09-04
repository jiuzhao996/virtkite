package handler

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/virt"
)

// volNameRegex 卷/池名称允许字母数字下划线连字符和点（卷名含扩展名）
var volNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// validVolName 校验卷/池名称合法性
func validVolName(name string) bool {
	return volNameRegex.MatchString(name)
}

// StorageHandler 存储池处理器
type StorageHandler struct {
	Virt *virt.Virt
}

// NewStorageHandler 创建存储池处理器
func NewStorageHandler() *StorageHandler {
	return &StorageHandler{Virt: virt.New()}
}

// ListPools 存储池列表（含详情）
func (h *StorageHandler) ListPools(c *gin.Context) {
	pools, err := h.Virt.ListPoolInfos()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取存储池失败", err)
		return
	}

	Success(c, gin.H{
		"total": len(pools),
		"items": pools,
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
	if req.Capacity <= 0 {
		req.Capacity = 20
	}

	if err := h.Virt.CreateVolumeCustom(poolName, req.Name, req.Format, req.Capacity); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"pool": poolName, "name": req.Name})
}

// DeleteVolume 删除存储池中的卷
func (h *StorageHandler) DeleteVolume(c *gin.Context) {
	poolName := c.Param("name")
	volName := c.Param("vol")
	if err := h.Virt.DeleteVolume(poolName, volName); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"pool": poolName, "vol": volName})
}
