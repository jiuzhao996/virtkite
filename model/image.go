package model

import (
	"time"

	"gorm.io/gorm"
)

// Image 镜像模型
type Image struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"size:100;not null" json:"name"`
	Path        string         `gorm:"size:500;not null" json:"path"`
	OSVersion   string         `gorm:"size:100" json:"os_version"`
	SizeGB      float64        `json:"size_gb"`
	Format      string         `gorm:"size:20;default:qcow2" json:"format"`
	IsTemplate  bool           `gorm:"default:false" json:"is_template"`
	Description string         `gorm:"type:text" json:"description"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 指定表名
func (Image) TableName() string {
	return "images"
}
