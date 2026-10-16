package handler

// crons_types.go 计划任务的请求/响应数据结构与内置模板：列表项、执行历史条目、
// 创建/更新请求体、出厂模板清单。行为逻辑见 crons.go，校验见 crons_validate.go。

import (
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/cron"
)

// cronTaskItem 列表项：任务本体 + 下次执行时间预览 + 最近执行记录。
type cronTaskItem struct {
	model.ScheduledTask
	NextRun    *time.Time    `json:"next_run"` // 表达式非法或 366 天内无匹配（如 2 月 31 日）时为 null
	RecentRuns []cronRunItem `json:"recent_runs"`
	// Stale 死开关命中：预期执行时刻已过宽限期仍无执行记录（调度器可能未运行）
	Stale    bool       `json:"stale"`
	StaleDue *time.Time `json:"stale_due,omitempty"`
}

// cronRunItem 执行历史条目（列表内嵌精简版；完整历史走 GET /api/crons/:id/runs）。
type cronRunItem struct {
	Status    string    `json:"status"`     // running / success / failed（model.CronRun 状态常量）
	StartedAt time.Time `json:"started_at"` // 执行开始时间
	Output    string    `json:"output"`     // 结果摘要：成功为成果描述、失败为错误摘要（≤2000 字符）
}

// cronTaskReq 创建/更新请求体。全部字段可选：Create 用默认值兜底，Update 只改出现的键，
// 校验始终针对「合并后的完整任务」做，避免部分更新造成 action 与 params 不配套。
type cronTaskReq struct {
	Name     *string `json:"name"`
	CronExpr *string `json:"cron_expr"`
	Action   *string `json:"action"`
	Params   *string `json:"params"`
	Enabled  *bool   `json:"enabled"`
	Keep     *int    `json:"keep"` // 保留最近 N 份产物（快照/备份），1-365
	// 死开关宽限（分钟，0=关闭，上限 1440）与失败重试（次数 0-3、间隔秒 1-120）
	GraceMinutes  *int `json:"grace_minutes"`
	RetryCount    *int `json:"retry_count"`
	RetryInterval *int `json:"retry_interval"`
	// 链式编排（A3）：成功后/失败后触发的下一跳任务 id。0 或 null 表示清除（不链）。
	OnSuccessTaskID *uint `json:"on_success_task_id"`
	OnFailureTaskID *uint `json:"on_failure_task_id"`
}

// cronTemplate 内置任务模板（出厂预设，前端「从模板新建」一键填充表单）。
// 需要目标（vm_id / playbook）的动作不放模板——无法预设，只能现选。
type cronTemplate struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	Desc          string `json:"desc"`
	Action        string `json:"action"`
	CronExpr      string `json:"cron_expr"`
	Params        string `json:"params"`
	Keep          int    `json:"keep"`
	GraceMinutes  int    `json:"grace_minutes"`
	RetryCount    int    `json:"retry_count"`
	RetryInterval int    `json:"retry_interval"`
}

// cronTemplates 出厂模板清单（顺序即前端展示顺序）。
var cronTemplates = []cronTemplate{
	{Key: "daily-db-backup", Name: "每日数据库备份", Desc: "每天 03:00 备份数据库，保留最近 7 份",
		Action: cron.ActionDBBackup, CronExpr: "0 3 * * *", Params: "{}", Keep: 7, GraceMinutes: 30, RetryInterval: 60},
	{Key: "hourly-healthcheck", Name: "每小时容器健康巡检", Desc: "每小时巡检容器健康，unhealthy 自动重启并通知",
		Action: cron.ActionContainerHealthcheck, CronExpr: "0 * * * *", Params: `{"notify":true}`, Keep: 7, GraceMinutes: 15, RetryInterval: 60},
	{Key: "daily-image-check", Name: "每日镜像版本巡检", Desc: "每天 08:00 检查本地镜像是否有新版可更新",
		Action: cron.ActionImageVersionCheck, CronExpr: "0 8 * * *", Params: "{}", Keep: 7, GraceMinutes: 60, RetryInterval: 60},
}
