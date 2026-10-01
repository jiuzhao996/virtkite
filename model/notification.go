package model

import "time"

// Notification 站内通知模型：告警触发时按「admin 全体 + VM 被授权人」路由到人
// （告警中心批次，2026-10）。与 alerts 表（告警事实记录，按 fingerprint 去重）互补：
// 本表是「某个用户收到了什么」的投递视图，同一告警可产生多行（每收件人一行）。
// 过程表不软删（与 tasks/audit_logs 同边界），由 startAlertRetention 定期清理过期行。
type Notification struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// UserID 收件人（users.id）；列表/已读接口按当前登录人过滤
	UserID uint `gorm:"not null;index:idx_notify_user_read,priority:1" json:"user_id"`
	// Title 通知标题（如「[告警] HostCpuHigh」）
	Title string `gorm:"size:200" json:"title"`
	// Content 通知正文（告警 summary/description，超长在入库前截断）
	Content string `gorm:"size:500" json:"content"`
	// Level 级别：critical / warning / info（映射告警 severity 标签，缺省 info）
	Level string `gorm:"size:16;default:info" json:"level"`
	// VMID 关联虚拟机（告警带 vm 标签且能匹配到库内 VM 时填，前端据此深链详情页）；指针：与告警无关的通知为 null
	VMID *uint `gorm:"index" json:"vm_id"`
	// AlertFingerprint 来源告警指纹（追溯到 alerts 表同一行；重复触发的告警各发一条通知，靠它关联）
	AlertFingerprint string `gorm:"size:64;index" json:"alert_fingerprint"`
	// Read 已读标记（未读数驱动顶栏铃铛红点）
	Read    bool      `gorm:"default:false;index:idx_notify_user_read,priority:2" json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名
func (Notification) TableName() string {
	return "notifications"
}

// 通知级别常量（与告警规则的 severity 标签取值对齐，未知值归一 info）
const (
	NotifyLevelCritical = "critical"
	NotifyLevelWarning  = "warning"
	NotifyLevelInfo     = "info"
)
