package handler

// vm_boot.go —— 引导相关操作：调整启动顺序、弹出安装介质。
//
// 背景：ISO 建机的引导顺序固定为 cdrom→hd（service/tasks/vm_create.go），
// 系统安装完成后若安装盘仍在光驱里，重启会再次进入安装程序。提供两个入口：
//   - SetBootOrder：把 hd 调到首位（显式控制，下次启动生效）；
//   - EjectCDROM：弹出安装介质（空光驱会被 BIOS 跳过，不依赖顺序调整）。

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/virt"
)

// SetBootOrder PUT /api/vms/:id/boot-order，body {"devices":["hd","cdrom"]}。
// 顺序即优先级；修改写入持久定义，运行中的虚拟机下次启动生效。
func (h *VMHandler) SetBootOrder(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	var req struct {
		Devices []string `json:"devices"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "请求参数不合法：devices 应为字符串数组")
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

	if err := h.Virt.SetBootOrder(vm.Name, req.Devices); err != nil {
		if errors.Is(err, virt.ErrInvalidBootOrder) {
			// 校验错误是自定义中文文案（设备名来自用户输入），可直接回显
			Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "devices": req.Devices})
}

// EjectCDROM POST /api/vms/:id/devices/cdrom/:target/eject
//
// 只弹介质、保留空光驱，不改引导顺序。光驱本来就空时返回 409（可纠正的状态，
// 不是服务端故障）。
func (h *VMHandler) EjectCDROM(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	target := c.Param("target")
	if target == "" {
		Fail(c, http.StatusBadRequest, "缺少光驱目标（target）")
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

	if err := h.Virt.EjectCDROM(vm.Name, target); err != nil {
		if errors.Is(err, virt.ErrNoMedia) {
			Fail(c, http.StatusConflict, err.Error())
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "target": target})
}
