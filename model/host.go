package model

import (
	"time"

	"gorm.io/gorm"
)

// Host 宿主机模型
type Host struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"size:100;not null" json:"name"`
	LibvirtURI  string         `gorm:"size:255;default:qemu:///system" json:"libvirt_uri"`
	SSHIP       string         `gorm:"size:45" json:"ssh_ip"`
	SSHPort     int            `gorm:"default:22" json:"ssh_port"`
	SSHUser     string         `gorm:"size:50;default:root" json:"ssh_user"`
	Status      string         `gorm:"size:20;default:unknown" json:"status"`
	CPUCores    int            `json:"cpu_cores"`
	MemoryGB    float64        `json:"memory_gb"`
	DiskGB      float64        `json:"disk_gb"`
	OSVersion   string         `gorm:"size:100" json:"os_version"`
	Description string         `gorm:"type:text" json:"description"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
	VMs         []VM           `gorm:"foreignKey:HostID" json:"vms,omitempty"`
}

// TableName 指定表名
func (Host) TableName() string {
	return "hosts"
}
