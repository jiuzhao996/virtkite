// Package cron 提供计划任务能力：5 字段 cron 表达式解析/匹配，以及整点 tick 的调度器。
//
// 与 robfig/cron 等三方库的取舍：本项目只需要「分 时 日 月 周」五字段的最小子集
// （无秒/年/时区/@daily 别名），纯函数约百行即可覆盖且可单测，不引第三方依赖。
// 匹配语义为五字段全 AND（与 robfig 标准版在 dom/dow 同时受限时的 OR 行为不同，
// 本项目场景不存在该用法），详见 Match 注释。
//
// 文件按职责拆分（纯搬运，无行为变更）：本文件是调度器核心（类型/生命周期/执行落库/
// 链式编排）；表达式解析与匹配见 cron_parse.go；各动作执行见 cron_actions.go；
// 执行记录簿记与死开关巡检见 cron_schedule.go。
package cron

import (
	"encoding/json"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

const (
	// maxRunOutput 失败时 cron_runs.output 保存的最大字符数（失败要留全量便于排障）。
	maxRunOutput = 2000
	// maxRunSummary 成功时 output 保存的最大字符数（成功只留摘要，防历史表膨胀）。
	maxRunSummary = 400
	// maxRetryCount / maxRetryIntervalSec 失败自动重试的上限（次数与间隔秒）。
	maxRetryCount       = 3
	maxRetryIntervalSec = 120
	// maxChainDepth 链式编排的最大深度（防环）。深到 5 跳还没停，多半是配错了。
	maxChainDepth = 5
	// prevPlaceholder 链式编排里引用上一跳结果的占位符（在 params 里替换）。
	prevPlaceholder = "{prev.output}"
)

// DefaultKeep 保留份数默认值：任务未配置 Keep（<=0，含历史行零值）时的兜底，
// 与 model.ScheduledTask.Keep 的 gorm default:7 一致。导出供 handler 创建任务时
// 取同一默认值，避免两处硬编码漂移。
const DefaultKeep = 7

// DefaultGrace 死开关宽限期默认值（分钟），与 model.ScheduledTask.GraceMinutes 的 gorm default:30 一致。
const DefaultGrace = 30

// DefaultRetryInterval 失败重试间隔默认值（秒），与 model 的 gorm default:60 一致。
const DefaultRetryInterval = 60

// NotifyURL 计划任务失败通知的 webhook 地址（飞书/钉钉等自定义机器人的 incoming 地址，
// 报文格式由 service/notify 按 URL 域名自动分派）。由 main 启动时注入（如环境变量
// CRON_NOTIFY_URL），为空表示不通知。用包级变量而非 Scheduler 字段：注入点与
// routes.go 内部的调度器构造解耦，main 一行即可接线。
var NotifyURL string

// Scheduler 计划任务调度器：整点 tick → 查 enabled 任务 → 表达式匹配 → 执行。
// 由 main 构造并调用 Start（内部自起 goroutine，进程生命周期即调度器生命周期）。
type Scheduler struct {
	DB        *gorm.DB
	Virt      *virt.Virt
	BackupDir string // 数据库备份输出目录，空串用 defaultBackupDir
	// TaskMgr ansible_playbook 动作的提交出口（P4-S3 调度×引擎缝合）：cron 只负责
	// 「什么时候跑」，执行细节（凭据/inventory/RECAP）全在 ansible_run 任务管线。
	// 注入接口而非具体类型会更好测，但当前只有这一种出口，先收口在 tasks.Manager。
	TaskMgr *tasks.Manager

	// DriftCheck 设计器漂移巡检回调（DE2）由 handler 侧注入（快照读取与平台现实
	// 扫描都在 handler：反向 import 会成环，与 NotifyURL 注入同款解法）。
	// 返回：已检查计划数、漂移计划数、人读摘要（进 cron_runs）。
	DriftCheck func() (checked int, drifted int, summary string)

	// execMu 串行化执行：定时 tick 与 handler 的手动触发（ExecuteNow）可能并发
	// 命中同一任务，mysqldump / 打快照不重入。ansible_playbook 转异步任务后本函数
	// 只做轮询等待（分钟级），同样占用此锁——并发到达的另一个任务会排队。
	execMu sync.Mutex

	// staleAlerted 死开关通知去重：任务 id → 已告警的预期时刻（同一预期时刻只推一次）。
	alertMu      sync.Mutex
	staleAlerted map[uint]time.Time
}

// Start 启动调度循环（内部自起 goroutine，无需再 go 一层）。
func (s *Scheduler) Start() {
	go s.loop()
}

// loop 调度主循环：sleep 到下一个整分（秒==0）执行一次 tick。
// 顶部 recover 兜底（项目规范：自起协程必须兜底）；单次 tick 的 panic 在 safeTick
// 内已被捕获并继续循环，本层 recover 只作最后防线，正常不可达。
func (s *Scheduler) loop() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[cron] 调度循环异常退出 panic=%v\n%s", r, debug.Stack())
		}
	}()
	for {
		next := minuteFloor(time.Now()).Add(time.Minute)
		time.Sleep(time.Until(next))
		s.safeTick(next)
	}
}

// safeTick 单次扫描，自带 recover：一个任务的 panic 不允许终结调度循环。
func (s *Scheduler) safeTick(now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[cron] tick panic 已兜底 now=%s panic=%v\n%s",
				now.Format("2006-01-02 15:04:05"), r, debug.Stack())
		}
	}()
	s.tick(now)
}

// tick 执行一轮调度扫描：取所有启用任务，表达式命中当前整分即执行；末尾跑死开关巡检。
func (s *Scheduler) tick(now time.Time) {
	s.sweepOrphanRuns()

	var tasks []model.ScheduledTask
	if err := s.DB.Where("enabled = ?", true).Find(&tasks).Error; err != nil {
		log.Printf("[cron] 查询计划任务失败: %v", err)
		return
	}
	for _, st := range tasks {
		spec, err := ParseCron(st.CronExpr)
		if err != nil {
			// 创建/更新时已校验，这里只在脏数据（手改库）时发生，记日志跳过
			log.Printf("[cron] 任务 %d(%s) 的 cron 表达式 %q 非法，已跳过: %v", st.ID, st.Name, st.CronExpr, err)
			continue
		}
		if !Match(spec, now) {
			continue
		}
		// 定时触发走防叠跑：上一轮未结束时本轮跳过而非排队
		if err := s.execute(st, true, 0); err != nil {
			log.Printf("[cron] 计划任务执行失败 id=%d(%s) action=%s: %v", st.ID, st.Name, st.Action, err)
		}
	}

	// 死开关：预期已过宽限期却无执行记录的任务，推送一次告警
	s.watchdog(s.StaleTasks())
}

// ExecuteNow 立即执行一次计划任务（handler 的手动触发入口），与定时执行共用 execute。
func (s *Scheduler) ExecuteNow(st model.ScheduledTask) error {
	// 手动触发不跳过：是显式的用户意图，即便有在跑的任务也排队等待（execMu 串行）
	return s.execute(st, false, 0)
}

// execute 执行单个任务并按其链式配置触发下一跳。
// depth 为链式深度（顶层调用传 0）；链式触发在 runOnce 释放锁之后进行，避免重入死锁。
func (s *Scheduler) execute(st model.ScheduledTask, skipIfRunning bool, depth int) error {
	// 防叠跑：存在未回写结束的 running 行即跳过。检查在取锁前做——排队到这里的上一轮已结束，不会误判；
	// 崩溃残留的孤儿行由 sweepOrphanRuns 在超 maxRunAge 后标记 failed，不会永久阻塞。
	if skipIfRunning && s.hasRunningRun(st.ID) {
		log.Printf("[cron] 任务 %d(%s) 上一轮尚未结束，本轮跳过", st.ID, st.Name)
		finishedAt := time.Now()
		skipped := model.CronRun{
			TaskID: st.ID, TaskName: st.Name, StartedAt: finishedAt, FinishedAt: &finishedAt,
			Status: model.CronRunStatusSkipped, Attempt: 1, Output: "上一轮执行尚未结束，本轮跳过",
		}
		if cerr := s.DB.Create(&skipped).Error; cerr != nil {
			log.Printf("[cron] 写跳过记录失败 id=%d: %v", st.ID, cerr)
		}
		return nil
	}

	summary, err := s.runOnce(st)

	// 链式编排：按本次结果触发下一跳（深度上限防环）。此处已在锁外，可安全再进 execute。
	s.maybeChain(st, summary, err, depth)
	return err
}

// runOnce 实际执行并落库执行历史/统计，返回摘要与错误。全程持锁（见 execMu）；
// 统计/历史写失败只记日志不阻断——下次执行仍会正常累计。
func (s *Scheduler) runOnce(st model.ScheduledTask) (string, error) {
	s.execMu.Lock()
	defer s.execMu.Unlock()

	log.Printf("[cron] 执行计划任务 id=%d name=%s action=%s", st.ID, st.Name, st.Action)

	// 执行历史：开始插一行 running（进程中断时残留的 running 行即中断痕迹，finished_at 为空）
	run := model.CronRun{
		TaskID:    st.ID,
		TaskName:  st.Name,
		StartedAt: time.Now(),
		Status:    model.CronRunStatusRunning,
		Attempt:   1,
	}
	if cerr := s.DB.Create(&run).Error; cerr != nil {
		log.Printf("[cron] 写执行历史（开始）失败 id=%d: %v", st.ID, cerr)
	}

	// 失败自动重试：次数/间隔均有上限（见常量）。重试期间持锁，会推迟其它任务（单机小任务量下可接受）。
	attempts := 1 + clampInt(st.RetryCount, 0, maxRetryCount)
	var summary string
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		run.Attempt = attempt
		summary, err = s.runAction(st)
		if err == nil || attempt == attempts {
			break
		}
		interval := clampInt(st.RetryInterval, 1, maxRetryIntervalSec)
		log.Printf("[cron] 任务 %d(%s) 第 %d 次失败，%d 秒后重试: %v", st.ID, st.Name, attempt, interval, err)
		time.Sleep(time.Duration(interval) * time.Second)
	}

	finishedAt := time.Now()
	run.FinishedAt = &finishedAt
	if err != nil {
		run.Status = model.CronRunStatusFailed
		run.Output = truncateRunes(err.Error(), maxRunOutput)
	} else {
		run.Status = model.CronRunStatusSuccess
		run.Output = truncateRunes(summary, maxRunSummary)
	}
	// 初始 Create 失败时 run.ID 为 0，Save 会退化成重新 INSERT 半截记录，故跳过
	if run.ID != 0 {
		if uerr := s.DB.Save(&run).Error; uerr != nil {
			log.Printf("[cron] 写执行历史（结束）失败 id=%d run_id=%d: %v", st.ID, run.ID, uerr)
		}
	}

	updates := map[string]interface{}{
		"last_run":  time.Now(),
		"run_count": gorm.Expr("run_count + 1"),
	}
	if uerr := s.DB.Model(&model.ScheduledTask{}).Where("id = ?", st.ID).Updates(updates).Error; uerr != nil {
		log.Printf("[cron] 更新执行统计失败 id=%d: %v", st.ID, uerr)
	}

	if err != nil {
		notifyFailure(st.Name, err.Error())
	}
	return summary, err
}

// maybeChain 按执行结果触发链式下一跳（成功→on_success，失败→on_failure）。
// 深度到达上限即停（防环）；下一跳不存在/已停用则跳过；params 里的 {prev.output}
// 用上一跳摘要做 JSON 字符串转义后替换（仅本次运行，不落库）。
func (s *Scheduler) maybeChain(st model.ScheduledTask, summary string, runErr error, depth int) {
	var nextID uint
	var prevOutput string
	if runErr != nil {
		if st.OnFailureTaskID != nil {
			nextID = *st.OnFailureTaskID
			prevOutput = runErr.Error()
		}
	} else if st.OnSuccessTaskID != nil {
		nextID = *st.OnSuccessTaskID
		prevOutput = summary
	}
	if nextID == 0 {
		return
	}
	if depth >= maxChainDepth {
		log.Printf("[cron] 链式深度已达上限 %d，任务 %d(%s) 不再下跳", maxChainDepth, st.ID, st.Name)
		return
	}
	var next model.ScheduledTask
	if err := s.DB.First(&next, nextID).Error; err != nil {
		log.Printf("[cron] 链式下一跳任务 %d 不存在（可能已删除），跳过: %v", nextID, err)
		return
	}
	if !next.Enabled {
		log.Printf("[cron] 链式下一跳任务 %d(%s) 已停用，跳过", next.ID, next.Name)
		return
	}
	if strings.Contains(next.Params, prevPlaceholder) {
		next.Params = strings.ReplaceAll(next.Params, prevPlaceholder, jsonEscapeString(prevOutput))
	}
	log.Printf("[cron] 链式触发：%d(%s) → %d(%s)（深度 %d）", st.ID, st.Name, next.ID, next.Name, depth+1)
	if err := s.execute(next, false, depth+1); err != nil {
		log.Printf("[cron] 链式任务执行失败 id=%d(%s): %v", next.ID, next.Name, err)
	}
}

// jsonEscapeString 把 s 转成可安全嵌进 JSON 字符串字面量的形式（去掉 json.Marshal 的外层引号）。
func jsonEscapeString(s string) string {
	b, err := json.Marshal(s)
	if err != nil || len(b) < 2 {
		return ""
	}
	return string(b[1 : len(b)-1])
}

// clampInt 把 v 夹到 [lo, hi]。
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// truncateRunes 按字符（rune）数截断到 limit，超限以省略号收尾——错误消息常含中文，
// 按字节截会切出半个字符的乱码。limit<=0 原样返回（防御）。
func truncateRunes(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	rs := []rune(s)
	return string(rs[:limit-1]) + "…"
}
