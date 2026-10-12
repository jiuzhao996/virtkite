package model

import "time"

// StackVersion 部署栈的 compose 版本快照（栈回滚用）。
//
// 为什么单列一张表：回滚要按栈随机读取历史 compose 内容，塞进 tasks.payload 既查不动、
// 也不便按保留策略清理；独立表 + 「只留最近 N 条」的裁剪（见 service/tasks/stack_versions.go）
// 是最直白的做法。
//
// 不软删：与 tasks/audit_logs 同属「过程记录」，保留策略直接物理删最旧行；若软删，
// 「只留最近 N 条」的裁剪与查询都得再带 deleted_at 条件，得不偿失（与 AGENTS 的
// 「过程表不软删」边界一致）。
type StackVersion struct {
	ID      uint   `gorm:"primaryKey" json:"id"`
	StackID string `gorm:"size:128;index;not null" json:"stack_id"` // 栈 id（=部署目录名=compose 项目名）
	// Version 栈内单调递增版本号（1 起）：用于「回滚到 vN」的展示与引用，各栈独立编号。
	Version int `gorm:"not null" json:"version"`
	// Content compose 文件全文（部署副本 data/stacks/<id>/docker-compose.yml 的内容）。
	// 用 mediumtext：大栈（ELK/Zabbix）的 compose 文件可达数十 KB，TEXT(64KB) 会截断。
	Content string `gorm:"type:mediumtext;not null" json:"content"`
	// Summary 版本摘要（「部署」「升级」「回滚到 v2」「回滚前快照」等，展示用）。
	Summary string `gorm:"size:200" json:"summary"`
	// Username / UserID 触发该版本的操作人（审计用；系统动作时为空）。
	Username string `gorm:"size:100" json:"username"`
	UserID   *uint  `json:"user_id"`
	// RollbackFrom 非空表示本版本由回滚产生，值为回滚的目标版本号（nil=普通部署/升级快照）。
	RollbackFrom *int      `json:"rollback_from,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// TableName 指定表名
func (StackVersion) TableName() string { return "stack_versions" }
