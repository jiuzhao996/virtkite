// vm_devices_health.go：虚拟机域定义健康检查（幽灵盘）与标准设备（guest-agent / virtio-rng）补齐。
package handler

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/virt"
)

// missingDiskSources 从域配置里挑出源文件不存在的磁盘（幽灵盘）。
// 返回条目与「是否存在致命项」——光驱 ISO 缺失不影响开机，单独标 fatal=false。
func missingDiskSources(spec *virt.DomainSpec) ([]gin.H, bool) {
	missing := []gin.H{}
	fatal := false
	if spec == nil {
		return missing, false
	}
	for _, d := range spec.Disks {
		if d.Source == "" {
			continue
		}
		if _, serr := os.Stat(d.Source); serr != nil {
			isFatal := d.Device == "disk"
			if isFatal {
				fatal = true
			}
			missing = append(missing, gin.H{
				"target": d.Target,
				"source": d.Source,
				"device": d.Device,
				"fatal":  isFatal,
			})
		}
	}
	return missing, fatal
}

// DomainHealth GET /api/vms/:id/domain-health —— 域定义健康检查。
// 目前只查一类问题：磁盘源文件不存在（幽灵盘）。它会让 libvirt 拒绝启动整个域，
// 而报错只出现在开机时刻、文案是「无法访问存储文件 + uid/gid」，与当初的加盘操作
// 完全脱节。此端点把该问题提前暴露在虚拟机详情页，并给出一键摘除的落点。
func (h *VMHandler) DomainHealth(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	spec, err := h.Virt.GetDomainSpec(vm.Name)
	if err != nil {
		// 域不存在（已删除/名字漂移）不是「不健康」，如实返回空结果而非 500
		Success(c, gin.H{"vm": vm.Name, "ok": true, "checked": false, "missing_disks": []gin.H{}})
		return
	}
	missing, fatal := missingDiskSources(spec)
	msg := ""
	if fatal {
		msg = "存在文件缺失的数据盘，虚拟机将无法开机（libvirt 会拒绝启动整个域），请摘除该磁盘或补齐文件"
	}
	Success(c, gin.H{
		"vm": vm.Name, "ok": !fatal, "checked": true,
		"missing_disks": missing,
		"message":       msg,
	})
}

// EnsureStandardDevices 补齐标准设备（guest-agent 通道 + virtio-rng），幂等。
// 用于把存量虚拟机配置对齐到新装机的标准设备集；响应返回本次实际添加的设备列表。
func (h *VMHandler) EnsureStandardDevices(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	attached, err := h.Virt.EnsureStandardDevices(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if len(attached) == 0 {
		Success(c, gin.H{"vm": vm.Name, "attached": attached, "message": "已是标准配置，无需补齐"})
		return
	}
	Success(c, gin.H{"vm": vm.Name, "attached": attached, "message": "已补齐：" + strings.Join(attached, "、")})
}
