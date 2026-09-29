package handler

// vm_power.go —— 虚拟机电源操作：启动 / 停止 / 重启 / 暂停 / 恢复
// （对应 virsh start / 优雅关机(stop_vm 任务) / reboot / suspend / resume）。
// 写操作统一先 guardVMIdle（无未终结后台任务）再 lockVM（并发互斥，见 vm.go）；
// StopVM 走异步任务根治优雅关机 15s 超时。生命周期入口见 vm_lifecycle.go。

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
)

// PauseVM 暂停虚拟机（对应 virsh suspend）。
func (h *VMHandler) PauseVM(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()
	if err := h.Virt.PauseDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	dbx.PersistBestEffort(h.DB, "pause_vm 回写状态", func() error {
		return h.DB.Model(&vm).Update("status", model.VMStatusPaused).Error
	})
	Success(c, gin.H{"vm": vm.Name, "message": "虚拟机已暂停"})
}

// ResumeVM 恢复已暂停的虚拟机（对应 virsh resume）。
func (h *VMHandler) ResumeVM(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()
	if err := h.Virt.ResumeDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	dbx.PersistBestEffort(h.DB, "resume_vm 回写状态", func() error {
		return h.DB.Model(&vm).Update("status", model.VMStatusRunning).Error
	})
	Success(c, gin.H{"vm": vm.Name, "message": "虚拟机已恢复"})
}

// StartVM 启动虚拟机
func (h *VMHandler) StartVM(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()

	// 调用 libvirt 启动（后台可能正跑 delete_vm/stop_vm，上面已先挡一层）
	if err := h.Virt.StartDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 更新状态（写失败重试并留痕：状态漂移会让仪表盘统计与批量判断失真）
	dbx.PersistBestEffort(h.DB, "start_vm 回写状态", func() error {
		return h.DB.Model(&vm).Update("status", model.VMStatusRunning).Error
	})

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已启动",
	})
}

// StopVM 停止虚拟机（异步：Submit stop_vm，后台执行优雅关机轮询，根治 15s 超时）。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
func (h *VMHandler) StopVM(c *gin.Context) {
	if !h.submitTaskGuard(c) {
		return
	}
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()
	payload := map[string]interface{}{"vm_id": vm.ID}
	userID, username := taskUserFromContext(c)
	vmID := vm.ID
	task, err := h.Tasks.Submit("stop_vm", "停止虚拟机 "+vm.Name, payload, userID, username, vm.Name, &vmID)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}

// RestartVM 重启虚拟机（对应 virsh reboot，需已运行）。
// 与 StopVM/DeleteVM 保持一致：下发前过 submitTaskGuard + guardVMIdle；
// 成功后按既有写法回写 vms.status——不回写会让状态机与 libvirt 实际状态不一致，
// 仪表盘统计与批量操作判断全部失真。
func (h *VMHandler) RestartVM(c *gin.Context) {
	if !h.submitTaskGuard(c) {
		return
	}
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()

	// 调用 libvirt 重启
	if err := h.Virt.RebootDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 回写状态：reboot 成功后域处于 running（常量值带空格，勿写字面量）
	// libvirt 已重启成功，DB 回写失败不翻成失败响应，只留痕（与 ListVMs 状态回写同口径）
	if err := h.DB.Model(&vm).Update("status", model.VMStatusRunning).Error; err != nil {
		LogError(c, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已重启",
	})
}
