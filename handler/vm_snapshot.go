package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ListSnapshots 获取虚拟机快照列表（返回 SnapshotInfo 详情数组，含 description/creation_time/state）。
func (h *VMHandler) ListSnapshots(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	snaps, err := h.Virt.ListSnapshots(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, snaps)
}

// CreateSnapshot 创建虚拟机快照（body: {name, description?}）。
func (h *VMHandler) CreateSnapshot(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "快照名称只允许字母、数字、下划线和连字符")
		return
	}

	if err := h.Virt.CreateSnapshot(vm.Name, req.Name, req.Description); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": req.Name})
}

// DeleteSnapshot 删除虚拟机快照
func (h *VMHandler) DeleteSnapshot(c *gin.Context) {
	snapName := c.Param("snap")
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	if err := h.Virt.DeleteSnapshot(vm.Name, snapName); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": snapName})
}

// RevertSnapshot 回滚虚拟机到指定快照
func (h *VMHandler) RevertSnapshot(c *gin.Context) {
	snapName := c.Param("snap")
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	if err := h.Virt.RevertSnapshot(vm.Name, snapName); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": snapName})
}
