package model

import (
	"time"
)

// AuditLog 审计日志模型
type AuditLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     *uint     `gorm:"index" json:"user_id"`
	Username   string    `gorm:"size:50" json:"username"`
	Action     string    `gorm:"size:50;not null;index" json:"action"`
	ObjectType string    `gorm:"size:50" json:"object_type"`
	ObjectID   *uint     `json:"object_id"`
	Detail     string    `gorm:"type:text" json:"detail"`
	SourceIP   string    `gorm:"size:45" json:"source_ip"`
	Status     string    `gorm:"size:20;default:success" json:"status"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
	User       *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// TableName 指定表名
func (AuditLog) TableName() string {
	return "audit_logs"
}
