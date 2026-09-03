package model

import (
	"time"

	"gorm.io/gorm"
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
	Status      string         `gorm:"size:20;default:shut_off" json:"status"`
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
