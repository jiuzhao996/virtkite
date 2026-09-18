package model

import "time"

// CronRun 计划任务执行历史：每次执行（定时命中或手动触发）在开始时插一行 running，
// 结束时回写 status 与 output 摘要。任务被删除后历史行保留（TaskName 为名称快照），
// 供任务列表的 recent_runs 与 GET /api/crons/:id/runs 执行历史展示、失败排障使用。
type CronRun struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	TaskID     uint       `gorm:"index" json:"task_id"`        // 所属计划任务 ID（scheduled_tasks.id）
	TaskName   string     `gorm:"size:100" json:"task_name"`   // 任务名快照（任务删除后历史仍可读）
	StartedAt  time.Time  `json:"started_at"`                  // 执行开始时间
	FinishedAt *time.Time `json:"finished_at"`                 // 结束时间（nil=执行中，或进程中断未回写）
	Status     string     `gorm:"size:20;index" json:"status"` // running / success / failed（常量见下）
	Output     string     `gorm:"type:text" json:"output"`     // 结果摘要：成功为成果描述、失败为错误摘要（≤2000 字符，超出截断）
}

// CronRun 状态常量（只允许这三个取值，写状态一律用常量不用字面量）。
// running 行残留说明执行中断（进程重启/崩溃），没有对应的结束回写。
const (
	CronRunStatusRunning = "running"
	CronRunStatusSuccess = "success"
	CronRunStatusFailed  = "failed"
)

// TableName 指定表名
func (CronRun) TableName() string { return "cron_runs" }
