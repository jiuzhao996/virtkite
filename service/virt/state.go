package virt

import (
	"github.com/digitalocean/go-libvirt"
)

// 平台状态字面量常量：StateToPlatform 的全部返回值。
// ⚠️ 必须与 model 包的 VMStatusRunning / VMStatusShutOff / VMStatusPaused / VMStatusError
// 逐字一致（含 "shut off" 中间的空格），前端状态映射同样按这批字面量匹配。
// 这里刻意不 import model：virt 是最底层的 libvirt 封装层，反向依赖 model 会把 GORM
// 拖进封装层并倒置分层依赖，因此两处各自定义、改动时手工同步（见 model/vm.go 同名注释）。
const (
	StatusRunning = "running"  // 运行中
	StatusShutOff = "shut off" // 已关机（空格，不是下划线）
	StatusPaused  = "paused"   // 已暂停
	StatusError   = "error"    // 异常
)

// StateToPlatform 将 libvirt 域状态枚举转换为平台 DB 中的 status 字符串。
// 保持与现有 vms.status 取值一致：running / shut off / paused / error。
// 等价 virsh：`virsh domstate <domain>`
func StateToPlatform(state int32) string {
	switch libvirt.DomainState(state) {
	case libvirt.DomainRunning:
		return StatusRunning
	case libvirt.DomainPaused:
		return StatusPaused
	case libvirt.DomainShutoff:
		return StatusShutOff
	case libvirt.DomainShutdown:
		return StatusShutOff
	case libvirt.DomainNostate, libvirt.DomainBlocked, libvirt.DomainCrashed, libvirt.DomainPmsuspended:
		return StatusError
	default:
		return StatusError
	}
}
