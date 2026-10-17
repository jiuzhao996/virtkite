// vm_interfaces.go：虚拟机网卡热插拔（attach/detach interface）。
// 磁盘热插拔见 vm_devices.go；两者同属生命周期写操作，均先 guardVMIdle 再 lockVM。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/virt"
)

// AttachInterface 添加网卡（对应 virsh attach-interface，运行中生效并落配置）。
// body: {interface: InterfaceSpec}；mac 为空自动生成。
func (h *VMHandler) AttachInterface(c *gin.Context) {
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

	var req struct {
		Interface virt.InterfaceSpec `json:"interface"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if req.Interface.Type == "" {
		req.Interface.Type = "network"
	}
	if req.Interface.Source == "" {
		req.Interface.Source = "default"
	}
	if req.Interface.MAC == "" {
		mac, err := virt.RandomMAC()
		if err != nil {
			ErrorWithMessage(c, http.StatusInternalServerError, "生成网卡 MAC 失败", err)
			return
		}
		req.Interface.MAC = mac
	}

	if err := h.Virt.AttachInterface(vm.Name, req.Interface); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "interface": req.Interface})
}

// DetachInterface 移除网卡（对应 virsh detach-interface，按 MAC 地址匹配）。
func (h *VMHandler) DetachInterface(c *gin.Context) {
	mac := c.Param("mac")
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
	if err := h.Virt.DetachInterface(vm.Name, mac); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "mac": mac})
}
