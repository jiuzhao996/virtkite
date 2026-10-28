// image_finalize.go：模板制作 / 镜像清洗入口——把一台关机的「制作机」清洗固化为模板。
// 清洗编排在 service/tasks 的 image_finalize executor（virt-sysprep → virt-customize →
// virt-sparsify → undefine → 登记模板）；本文件只做参数校验与任务提交。
package handler

import (
	"net/http"
	"strings"

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
		Name         string `json:"name"`
		Description  string `json:"description"`
		OSVersion    string `json:"os_version"`
		CustomScript string `json:"custom_script"`
		Optimize     *bool  `json:"optimize"`
		Sparsify     *bool  `json:"sparsify"`
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
	if strings.TrimSpace(req.CustomScript) != "" {
		payload["custom_script"] = req.CustomScript
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
		SubmitTaskError(c, err)
		return
	}
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}

// FinalizeCapability GET /api/vms/:id/finalize-capability
// 供前端判断「固化为模板」按钮是否可用，并在弹窗里给出两条前置提醒：
//   - guestfs_available / vm_shutoff：硬性前提（工具链就绪 + 已关机）
//   - has_snapshot：固化是原地清洗原盘，快照保留的是清洗前旧状态，模板不该带制作机历史
//   - boot_cdrom_first：ISO 建机默认 cdrom→hd，装完系统不切回来，重启会重复进安装程序
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
		"has_snapshot":      false,
		"snapshot_count":    0,
		"boot_cdrom_first":  false,
	}
	if t, err := guestfs.Detect(); err != nil {
		resp["reason"] = err.Error()
	} else {
		resp["guestfs_available"] = true
		resp["version"] = t.Version
	}
	// 快照探测：查不到（如域不存在）不阻断，按"无快照"处理
	if snaps, err := h.Virt.ListSnapshots(vm.Name); err == nil {
		resp["has_snapshot"] = len(snaps) > 0
		resp["snapshot_count"] = len(snaps)
	}
	// 启动顺序探测：光驱排在硬盘前面才算"光驱优先"
	if order, err := h.Virt.BootOrder(vm.Name); err == nil && len(order) > 0 {
		resp["boot_order"] = order
		for _, d := range order {
			if d == "hd" {
				break
			}
			if d == "cdrom" {
				resp["boot_cdrom_first"] = true
				break
			}
		}
	}
	Success(c, resp)
}
