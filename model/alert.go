package model

import "time"

// Alert 告警历史模型：Alertmanager webhook 推回的告警按 fingerprint 去重入库，
// firing/resolved 覆盖式更新同一行，供「监控中心 → 告警历史」追溯查询。
// 与实时告警列表（代理 Alertmanager /api/v2/alerts，不留痕）互补：平台重启/AM 环形
// 截断后实时列表查不到的告警，这里仍有记录。
type Alert struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// Fingerprint Alertmanager 对同一条告警（标签组合）算出的稳定指纹，去重主键
	Fingerprint string `gorm:"size:64;uniqueIndex" json:"fingerprint"`
	// Labels / Annotations 原始 JSON 文本（结构随告警规则变化，不建强类型列），查询时反序列化
	Labels      string `gorm:"type:text" json:"labels"`
	Annotations string `gorm:"type:text" json:"annotations"`
	Status      string `gorm:"size:20" json:"status"` // firing / resolved
	// StartsAt 告警开始时间（Alertmanager 必带）
	StartsAt time.Time `json:"starts_at"`
	// EndsAt 恢复时间：必须用指针——firing 告警不携带该字段，值类型零值会写成 MySQL 零日期
	// '0000-00-00'，在严格 SQL 模式（NO_ZERO_DATE）下直接插入失败（实测 Error 1292）
	EndsAt    *time.Time `json:"ends_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// TableName 指定表名
func (Alert) TableName() string {
	return "alerts"
}

// 告警状态常量（与 Alertmanager webhook payload 的 status 字段取值一致）
const (
	AlertStatusFiring   = "firing"
	AlertStatusResolved = "resolved"
)
