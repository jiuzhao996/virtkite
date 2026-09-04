package virt

import (
	"github.com/digitalocean/go-libvirt"
)

// StateToPlatform 将 libvirt 域状态枚举转换为平台 DB 中的 status 字符串。
// 保持与现有 vms.status 取值一致：running / shut off / paused / error。
func StateToPlatform(state int32) string {
	switch libvirt.DomainState(state) {
	case libvirt.DomainRunning:
		return "running"
	case libvirt.DomainPaused:
		return "paused"
	case libvirt.DomainShutoff:
		return "shut off"
	case libvirt.DomainShutdown:
		return "shut off"
	case libvirt.DomainNostate, libvirt.DomainBlocked, libvirt.DomainCrashed, libvirt.DomainPmsuspended:
		return "error"
	default:
		return "error"
	}
}
