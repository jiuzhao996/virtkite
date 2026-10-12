package model

import (
	"time"
)

// 任务状态常量（tasks 包内有同值非导出常量；metrics 等外部消费方从这里取）。
const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusSuccess   = "success"
	TaskStatusFailed    = "failed"
	TaskStatusCancelled = "cancelled"
)

// 任务日志条目类型（TaskLogEntry.Kind 取值，对应 Coolify/Semaphore 的结构化日志）。
const (
	TaskLogKindStdout  = "stdout"
	TaskLogKindStderr  = "stderr"
	TaskLogKindCommand = "command"
	TaskLogKindInfo    = "info"
)

// TaskLogEntry 单条结构化任务日志（tasks.log_entries 列里 JSON 数组的元素）。
//
// 为什么要有它：旧实现把任务输出拼成一整段纯文本塞进 result，前端只能整块展示，
// 既无法分色（stdout/stderr）、也无法折叠调试噪音。升级成条目数组后，前端可按 kind
// 分色、按 hidden 折叠、按 seq 增量去重（配合日志 WS 追平）。
type TaskLogEntry struct {
	// Seq 任务内单调递增序号（从 1 起，跨重试沿用同一序列）：WS 增量按它去重/断点续传。
	Seq int `json:"seq"`
	// Ts 产生时间（Unix 毫秒）。
	Ts int64 `json:"ts"`
	// Kind 条目类型：stdout|stderr|command|info。
	Kind string `json:"kind"`
	// Text 日志正文（落库前已脱敏）。
	Text string `json:"text"`
	// Hidden=true 的调试/噪音行前端默认折叠，可展开。
	Hidden bool `json:"hidden,omitempty"`
}

// Task 异步任务模型（对应 JumpServer/PVE task 队列语义）。
type Task struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Type     string `gorm:"size:50;index" json:"type"`
	Title    string `gorm:"size:200" json:"title"`
	Status   string `gorm:"size:20;index" json:"status"`
	Progress int    `gorm:"default:0" json:"progress"`
	Payload  string `gorm:"type:text" json:"-"`
	Result   string `gorm:"type:text" json:"result,omitempty"`
	Error    string `gorm:"size:500" json:"error,omitempty"`
	// LogEntries 结构化执行日志（JSON 数组，元素为 TaskLogEntry）。
	// json:"-" 刻意不进列表/详情响应：列表一次几十行，每行带整段日志会把响应撑爆；
	// 日志改由专用端点 GET /api/tasks/:id/logs 与日志 WS 下发。
	// 类型用 mediumtext 而非 text：条目数组（含 JSON 包裹与转义）比纯文本更大，64KB 的
	// TEXT 会截断，导致日志末尾丢失且 JSON 解析失败。
	LogEntries string `gorm:"type:mediumtext" json:"-"`
	// RetryCount 已自动重试次数；MaxRetry 该任务允许的自动重试上限（0=不自动重试）。
	// MaxRetry 在 Submit 时按重试白名单写入，只有幂等安全的类型才 > 0（见 service/tasks/retry.go）。
	RetryCount int `gorm:"default:0" json:"retry_count,omitempty"`
	MaxRetry   int `gorm:"default:0" json:"max_retry,omitempty"`
	// RetryState 重试扫描器的行级 CAS 状态（""=空闲可认领，"claiming"=已被扫描器认领，
	// "aborted"=进程重启清扫后不再重试）。仅内部并发控制用，不下发前端。
	RetryState string    `gorm:"size:20" json:"-"`
	UserID     *uint     `json:"user_id"`
	Username   string    `gorm:"size:100" json:"username"`
	VMID       *uint     `gorm:"index" json:"vm_id,omitempty"`
	VMName     string    `gorm:"size:100" json:"vm_name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// TableName 指定表名
func (Task) TableName() string {
	return "tasks"
}
