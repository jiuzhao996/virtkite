package handler

// vm_spec.go —— 虚拟机规格与配置调整：vCPU / 内存 / 开机自启。
// vCPU/内存停机态走「读 spec → 重建 XML → 重 define」，运行态走 live API；
// libvirt 侧生效后回写 DB，回写失败按「部分成功」显式暴露（配置漂移需人工同步，
// vms.v_cpu 才是克隆/容量统计/备份还原的依据）。电源操作见 vm_power.go。

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/virt"
)

// SetVcpu 调整 CPU 核数（对应 virsh setvcpus），同步 DB。
// 停机态通过重 define 修改持久配置（setvcpus CONFIG 无法超 <vcpu> 上限）；
// 运行态走 live API（仅可调至启动时最大核数以内，超出提示关机）。
func (h *VMHandler) SetVcpu(c *gin.Context) {
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
		VCPU int `json:"vcpu"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.VCPU <= 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "vCPU 数量必须大于 0", err)
		return
	}

	// 停机态：读取 spec → 改 vcpu → 重建 XML → 重 define
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != virt.StatusRunning {
		spec, err := h.Virt.GetDomainSpec(vm.Name)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		spec.VCPU = req.VCPU
		spec.RawXML = ""
		xmlstr, err := virt.BuildDomainXML(spec)
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
			return
		}
		if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	} else {
		// 运行态：live+config 热调（超出启动时最大核数由 libvirt 报错，翻译提示）
		if err := h.Virt.SetVcpus(vm.Name, req.VCPU); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	}
	// libvirt 侧已生效，DB 回写失败 = 持久配置漂移（vms.v_cpu 才是克隆/容量统计/备份还原的依据）：
	// 不能只 LogError 让前端看到纯成功，否则运维无从知道要补一次同步。
	// 列名必须写结构体字段对应的 v_cpu（不是字段名 vcpu），写错会每次都回写失败且同样被静默吞掉。
	if err := h.DB.Model(&vm).Update("v_cpu", req.VCPU).Error; err != nil {
		LogError(c, fmt.Errorf("[配置漂移-需同步] 虚拟机 %s 的 vCPU 已在 libvirt 调整为 %d，但回写数据库失败: %w",
			vm.Name, req.VCPU, err))
		Created(c, "vCPU 已在虚拟机上生效，但记录到数据库失败，请重试或联系管理员同步",
			gin.H{"vm": vm.Name, "vcpu": req.VCPU, "db_synced": false})
		return
	}
	Success(c, gin.H{"vm": vm.Name, "vcpu": req.VCPU, "db_synced": true})
}

// SetMemory 调整内存（对应 virsh setmem），同步 DB。
// 停机态通过重 define 修改持久配置；运行态走 live API（仅可调至启动时最大内存以内）。
func (h *VMHandler) SetMemory(c *gin.Context) {
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
		MemoryMB int `json:"memory_mb"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.MemoryMB <= 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "内存大小必须大于 0", err)
		return
	}

	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != virt.StatusRunning {
		spec, err := h.Virt.GetDomainSpec(vm.Name)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		spec.MemoryMB = req.MemoryMB
		spec.RawXML = ""
		xmlstr, err := virt.BuildDomainXML(spec)
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
			return
		}
		if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	} else {
		if err := h.Virt.SetMemory(vm.Name, req.MemoryMB); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	}
	// 同 SetVcpu：回写失败必须显式暴露为「部分成功」，见该函数注释。
	if err := h.DB.Model(&vm).Update("memory_mb", req.MemoryMB).Error; err != nil {
		LogError(c, fmt.Errorf("[配置漂移-需同步] 虚拟机 %s 的内存已在 libvirt 调整为 %dMB，但回写数据库失败: %w",
			vm.Name, req.MemoryMB, err))
		Created(c, "内存已在虚拟机上生效，但记录到数据库失败，请重试或联系管理员同步",
			gin.H{"vm": vm.Name, "memory_mb": req.MemoryMB, "db_synced": false})
		return
	}
	Success(c, gin.H{"vm": vm.Name, "memory_mb": req.MemoryMB, "db_synced": true})
}

// SetAutostart 设置开机自启（对应 virsh autostart）。
func (h *VMHandler) SetAutostart(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if err := h.Virt.SetAutostart(vm.Name, req.Enabled); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "autostart": req.Enabled})
}
