package model

import "time"

// ScheduledTask 计划任务：按 cron 表达式定时执行的动作（定时快照 / 定时备份数据库）。
// 调度器在 service/cron（整点 tick + 表达式匹配），本表只存定义与执行统计。
type ScheduledTask struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Name      string     `gorm:"size:100;not null" json:"name"`     // 任务名（展示用）
	CronExpr  string     `gorm:"size:50;not null" json:"cron_expr"` // 5 字段 cron 表达式：分 时 日 月 周
	Action    string     `gorm:"size:50;not null" json:"action"`    // 动作类型：vm_snapshot / db_backup
	Params    string     `gorm:"size:500" json:"params"`            // JSON 参数：vm_snapshot→{"vm_id":14}；db_backup→{}（保留）
	Enabled   bool       `gorm:"default:true" json:"enabled"`       // 启停开关（关闭后调度器跳过）
	Keep      int        `gorm:"default:7" json:"keep"`             // 保留最近 N 份产物：vm_snapshot=cron- 前缀快照数 / db_backup=备份文件数；<=0 视为 7
	LastRun   *time.Time `json:"last_run"`                          // 最近一次执行时间（含手动触发；nil=从未执行）
	// 死开关（dead man's switch）：最近一次预期执行时刻过了宽限期仍无执行记录即告警。
	// <=0 或 >1440 表示关闭；典型值 30（半小时）。
	GraceMinutes int `gorm:"default:30" json:"grace_minutes"`
	// 失败自动重试：次数（0=不重试，上限 3）与间隔秒数（上限 120）。
	// 注意：重试在 execute 持锁期间 sleep，会推迟其它任务的执行（单机小任务量下可接受）。
	RetryCount    int `gorm:"default:0" json:"retry_count"`
	RetryInterval int `gorm:"default:60" json:"retry_interval"`
	RunCount  int        `gorm:"default:0" json:"run_count"`        // 累计执行次数（成功失败都计）
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// TableName 指定表名
func (ScheduledTask) TableName() string { return "scheduled_tasks" }
