package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// GetVMSpec 返回虚拟机完整配置（DB 记录 + DomainSpec，spec 含 raw_xml 回显）。
func (h *VMHandler) GetVMSpec(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.Preload("Host").First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	// 同步 libvirt 状态
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != "" {
		vm.Status = state
		h.DB.Model(&vm).Update("status", state)
	}

	spec, err := h.Virt.GetDomainSpec(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm, "spec": spec})
}

// UpdateVMSpec 整体重 define 虚拟机配置（对应 virsh edit 后 define）。
// 请求体为完整 DomainSpec（raw_xml 忽略）；VM 运行中禁止修改，须先关机。
// 同步回写 DB 的 vcpu / memory_mb / disk_gb（首个磁盘容量近似）/ mac_address（首个网卡）。
func (h *VMHandler) UpdateVMSpec(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	var spec virt.DomainSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	// 运行时禁止整体重定义，提示先关机
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state == virt.StatusRunning {
		Fail(c, http.StatusBadRequest, "虚拟机运行中，请先关机后再修改配置")
		return
	}

	// 名称/UUID 以 DB 为准，防止定义错位；raw_xml 由 BuildDomainXML 重建，忽略回显原文
	spec.Name = vm.Name
	if spec.UUID == "" {
		spec.UUID = vm.UUID
	}
	spec.CloudInit = nil
	spec.RawXML = ""

	xmlstr, err := virt.BuildDomainXML(&spec)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
		return
	}
	if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 同步 DB 摘要字段
	updates := map[string]interface{}{"vcpu": spec.VCPU, "memory_mb": spec.MemoryMB}
	if len(spec.Disks) > 0 {
		if gb := h.Virt.DiskSizeGB(spec.Disks[0].Source); gb > 0 {
			updates["disk_gb"] = gb
		}
	}
	if len(spec.Interfaces) > 0 && spec.Interfaces[0].MAC != "" {
		updates["mac_address"] = spec.Interfaces[0].MAC
	}
	// libvirt 侧已 define 成功，DB 摘要回写失败会产生持久不一致，留痕不回滚
	if err := h.DB.Model(&vm).Updates(updates).Error; err != nil {
		LogError(c, err)
	}

	Success(c, gin.H{"vm": vm.Name, "message": "配置已更新"})
}

// GetVMXML 获取虚拟机 XML 定义
func (h *VMHandler) GetVMXML(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	xml, err := h.Virt.GetDomainXML(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": vm.Name, "xml": xml})
}

// UpdateVMXML 更新虚拟机 XML 定义（高级功能）
func (h *VMHandler) UpdateVMXML(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	var req struct {
		XML string `json:"xml" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	if err := h.Virt.UpdateDomainXML(vm.Name, req.XML); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": vm.Name, "message": "XML 已更新"})
}
