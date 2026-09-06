package handler

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/virt"
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
// 目录池的 path 决定该池所有卷的落盘位置，零校验等于让调用方指定宿主机任意目录，
// 因此要求：绝对路径 + 字符白名单 + 规范写法（filepath.Clean 后与原值一致，
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
