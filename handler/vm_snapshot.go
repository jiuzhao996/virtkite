package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// validSnapshotName 校验快照名称（路径参数 :snap）的字符白名单。
//
// 为什么要在 handler 侧显式校验：快照名是字符串主键，不走 paramID 的数值化路径，
// 最终会以 DomainSnapshotLookupByName 的入参进入 libvirt。虽然 virt 层已有
// xmlEscape + LookupByName 兜底且当前无实际注入，但把覆盖留在一层 = 换个组合就能绕。
// 复用 storage.go 的 volNameRegex（字母数字下划线连字符点），不另造同型正则；
// 长度上限 100 与 model.VM.Name 的 size:100 对齐，避免超长串打到 libvirt。
func validSnapshotName(name string) bool {
	return len(name) > 0 && len(name) <= 100 && volNameRegex.MatchString(name)
}

// snapNameParam 取并校验路径参数 :snap，非法时已写好 400 响应并返回 ok=false。
func snapNameParam(c *gin.Context) (string, bool) {
	snapName := c.Param("snap")
	if !validSnapshotName(snapName) {
		Fail(c, http.StatusBadRequest, "快照名称只允许字母、数字、下划线、连字符和点")
		return "", false
	}
	return snapName, true
}

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
	snapName, ok := snapNameParam(c)
	if !ok {
		return
	}
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
	snapName, ok := snapNameParam(c)
	if !ok {
		return
	}
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
