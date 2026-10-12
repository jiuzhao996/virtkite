package model

import (
	"time"
)

// AuditLog 审计日志模型
type AuditLog struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	UserID     *uint  `gorm:"index" json:"user_id"`
	Username   string `gorm:"size:50" json:"username"`
	Action     string `gorm:"size:50;not null;index" json:"action"`
	ObjectType string `gorm:"size:50" json:"object_type"`
	ObjectID   *uint  `json:"object_id"`
	Detail     string `gorm:"type:text" json:"detail"`
	// BeforeData / AfterData 变更前后的对象快照（JSON，脱敏后）。
	// 只有显式调用 middleware.SetAuditDiff 的写操作才有值——中间件本身只拿得到
	// method/path，**没有对象上下文**（NetBox 靠 Django 信号自动快照，我们这套
	// Gin 中间件拿不到，硬套只会得到空快照）。留空表示该操作未做对象级快照。
	BeforeData string    `gorm:"type:text" json:"before_data,omitempty"`
	AfterData  string    `gorm:"type:text" json:"after_data,omitempty"`
	SourceIP   string    `gorm:"size:45" json:"source_ip"`
	Status     string    `gorm:"size:20;default:success" json:"status"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

// TableName 指定表名
func (AuditLog) TableName() string {
	return "audit_logs"
}
