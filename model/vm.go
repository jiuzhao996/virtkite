package model

import (
	"time"

	"gorm.io/gorm"
)

// 虚拟机状态常量：vms.status 列的全部合法取值。
// ⚠️ 取值必须与 service/virt.StateToPlatform 的四个返回值逐字一致（含空格），
// 前端状态色/文案映射（VmList.vue / VmDetail.vue / Dashboard.vue）也按同一批字面量匹配。
// virt 是底层 libvirt 封装层，不反向依赖 model（否则把 GORM 拖进封装层并倒置分层），
// 因此两处各自定义常量，改动任一处必须同步另一处。
const (
	VMStatusRunning = "running"  // 运行中（libvirt DomainRunning）
	VMStatusShutOff = "shut off" // 已关机（libvirt DomainShutoff/DomainShutdown），注意是空格不是下划线
	VMStatusPaused  = "paused"   // 已暂停（libvirt DomainPaused）
	VMStatusError   = "error"    // 异常（nostate/blocked/crashed/pmsuspended 等）
)

// VM 虚拟机模型
type VM struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	UUID        string         `gorm:"size:36;uniqueIndex" json:"uuid"`
	Name        string         `gorm:"size:100;not null" json:"name"`
	HostID      uint           `gorm:"not null;index" json:"host_id"`
	Template    string         `gorm:"size:100" json:"template"`
	StoragePool string         `gorm:"size:100;default:vmops" json:"storage_pool"`
	VCPU        int            `gorm:"default:1" json:"vcpu"`
	MemoryMB    int            `gorm:"default:1024" json:"memory_mb"`
	DiskGB      int            `gorm:"default:20" json:"disk_gb"`
	IP          string         `gorm:"size:45" json:"ip"`
	MACAddress  string         `gorm:"size:17" json:"mac_address"`
	Status      string         `gorm:"size:20;default:shut off" json:"status"` // gorm tag 内不能引用常量，此处字面量必须与 VMStatusShutOff 同步
	OSType      string         `gorm:"size:50" json:"os_type"`
	Description string         `gorm:"type:text" json:"description"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
	Host        Host           `gorm:"foreignKey:HostID" json:"host,omitempty"`
}

// TableName 指定表名
func (VM) TableName() string {
	return "vms"
}
