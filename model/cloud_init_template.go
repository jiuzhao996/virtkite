package model

import "time"

// CloudInitTemplate cloud-init 配置模板（v3 批次 L）。
// Spec 存 JSON 序列化的 virt.CloudInitSpec（hostname/user/password/ssh_key/net_mode/ip/gateway/dns），
// 前端「套用模板」时把该对象回填进创建向导的 cloud-init 表单，建机 payload 原样透传。
// virt 层刻意不 import model（避免把 GORM 拖进最底层封装层，见 service/virt 约定），
// 故这里以 JSON 字符串松耦合持有，反序列化在 handler 层完成。
type CloudInitTemplate struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:100;not null;uniqueIndex" json:"name"` // 模板名，唯一
	Spec        string    `gorm:"type:text;not null" json:"-"`               // JSON 序列化的 virt.CloudInitSpec（对外经 DTO 以对象返回，见 handler）
	Description string    `gorm:"size:500" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 指定表名（GORM 默认复数推导与本名一致，显式声明防歧义）。
func (CloudInitTemplate) TableName() string { return "cloud_init_templates" }
