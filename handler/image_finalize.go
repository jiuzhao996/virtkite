// image_finalize.go：模板制作 / 镜像清洗入口——把一台关机的「制作机」清洗固化为模板。
// 清洗编排在 service/tasks 的 image_finalize executor（virt-sysprep → virt-customize →
// virt-sparsify → undefine → 登记模板）；本文件只做参数校验与任务提交。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/guestfs"
)

// FinalizeImage POST /api/vms/:id/finalize-image
// 把关机的制作机清洗固化为模板（异步：202 {task_id}，前端轮询 GET /api/tasks/:id）。
// body（可空）：{name?, description?, os_version?, optimize?, sparsify?}。
func (h *VMHandler) FinalizeImage(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	if !h.submitTaskGuard(c) {
		return
	}
	// 关机校验（清洗直接读写镜像文件，运行中执行会损坏磁盘——与离线挂载同口径）
	if vm.Status != model.VMStatusShutOff {
		Fail(c, http.StatusConflict, "虚拟机未关机，请先关机再固化为模板")
		return
	}
	// 工具可用性真自检：不可用挡在提交前，别让任务跑一半才失败
	if _, err := guestfs.Detect(); err != nil {
		ErrorWithMessage(c, http.StatusServiceUnavailable, "镜像清洗工具不可用", err)
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		OSVersion   string `json:"os_version"`
		Optimize    *bool  `json:"optimize"`
		Sparsify    *bool  `json:"sparsify"`
	}
	_ = c.ShouldBindJSON(&req) // body 可空，绑定失败按默认处理
	payload := map[string]interface{}{"vm_id": vm.ID}
	if req.Name != "" {
		payload["name"] = req.Name
	}
	if req.Description != "" {
		payload["description"] = req.Description
	}
	if req.OSVersion != "" {
		payload["os_version"] = req.OSVersion
	}
	if req.Optimize != nil {
		payload["optimize"] = *req.Optimize
	}
	if req.Sparsify != nil {
		payload["sparsify"] = *req.Sparsify
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("image_finalize", "固化为模板 "+vm.Name, payload, userID, username, vm.Name, &vm.ID)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}

// FinalizeCapability GET /api/vms/:id/finalize-capability
// 供前端判断「固化为模板」按钮是否可用：工具链是否就绪（真自检）+ VM 是否关机。
func (h *VMHandler) FinalizeCapability(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	resp := gin.H{
		"guestfs_available": false,
		"version":           "",
		"reason":            "",
		"vm_shutoff":        vm.Status == model.VMStatusShutOff,
	}
	if t, err := guestfs.Detect(); err != nil {
		resp["reason"] = err.Error()
	} else {
		resp["guestfs_available"] = true
		resp["version"] = t.Version
	}
	Success(c, resp)
}
