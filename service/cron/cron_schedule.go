// 本文件负责计划任务的调度簿记与告警：执行记录的防叠跑判定与孤儿清理、"死开关"（预期
// 时刻已过宽限期却无执行记录）巡检，以及失败/死开关的通知推送。所有函数都无 HTTP 入口，
// 由调度器 tick 或 handler 列表标记调用。
package cron

import (
	"fmt"
	"log"
	"runtime/debug"
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/notify"
)

const (
	// notifyErrLimit 失败通知消息里错误摘要的最大字符数。
	notifyErrLimit = 200
	// maxGraceMinutes 死开关宽限期的上限（分钟）；超过视为关闭，防逐分钟回看退化成大循环。
	maxGraceMinutes = 1440
	// maxRunAge 超过此时长仍未回写结束状态的 running 行视为孤儿（进程崩溃残留）。
	maxRunAge = 2 * time.Hour
)

// hasRunningRun 该任务是否存在未回写结束状态的 running 行（超 maxRunAge 的孤儿行不算，避免永久阻塞）。
// 查询失败按「无并发」处理——宁可执行也不误跳过。
func (s *Scheduler) hasRunningRun(taskID uint) bool {
	var n int64
	cutoff := time.Now().Add(-maxRunAge)
	if err := s.DB.Model(&model.CronRun{}).
		Where("task_id = ? AND status = ? AND finished_at IS NULL AND started_at > ?",
			taskID, model.CronRunStatusRunning, cutoff).Count(&n).Error; err != nil {
		return false
	}
	return n > 0
}

// sweepOrphanRuns 把超过 maxRunAge 仍未回写结束状态的 running 行扫成 failed。
// 进程崩溃/被 kill 时正在执行的行会残留，不清理会永久阻塞该任务的防叠跑判定。
func (s *Scheduler) sweepOrphanRuns() {
	cutoff := time.Now().Add(-maxRunAge)
	res := s.DB.Model(&model.CronRun{}).
		Where("status = ? AND finished_at IS NULL AND started_at < ?", model.CronRunStatusRunning, cutoff).
		Updates(map[string]interface{}{
			"status":      model.CronRunStatusFailed,
			"finished_at": time.Now(),
			"output":      "执行中断（进程重启或崩溃），未回写结束状态",
		})
	if res.Error != nil {
		log.Printf("[cron] 清理残留执行记录失败: %v", res.Error)
	} else if res.RowsAffected > 0 {
		log.Printf("[cron] 已把 %d 条残留 running 执行记录标记为 failed", res.RowsAffected)
	}
}

// lastDue 返回 <= now 的最近一次表达式命中时刻（从当前整分逐分钟回看 graceMin+1 分钟，
// 覆盖「预期点已被宽限期跨过」的场景）；窗口内无命中返回零值。
func lastDue(spec *Spec, now time.Time, graceMin int) time.Time {
	for i := 0; i <= graceMin+1; i++ {
		t := minuteFloor(now).Add(-time.Duration(i) * time.Minute)
		if Match(spec, t) {
			return t
		}
	}
	return time.Time{}
}

// StaleInfo 死开关命中信息。
type StaleInfo struct {
	Name string
	Due  time.Time
}

// StaleTasks 返回「最近一次预期执行时刻已过宽限期、却无对应执行记录」的启用任务（键为任务 ID）。
// grace<=0 或 >maxGraceMinutes 视为关闭死开关。供调度器 tick 告警与 handler 列表标记共用。
func (s *Scheduler) StaleTasks() map[uint]StaleInfo {
	out := map[uint]StaleInfo{}
	var tasks []model.ScheduledTask
	if err := s.DB.Where("enabled = ?", true).Find(&tasks).Error; err != nil {
		return out
	}
	now := time.Now()
	for _, st := range tasks {
		if st.GraceMinutes <= 0 || st.GraceMinutes > maxGraceMinutes {
			continue
		}
		spec, err := ParseCron(st.CronExpr)
		if err != nil {
			continue
		}
		grace := time.Duration(st.GraceMinutes) * time.Minute
		due := lastDue(spec, now, st.GraceMinutes)
		if due.IsZero() {
			continue // 回看窗口内无预期时刻：周期长于宽限期，暂不判定
		}
		if !st.CreatedAt.Before(due) {
			continue // 预期时刻早于任务创建（首个周期），不算漏跑
		}
		if now.Before(due.Add(grace)) {
			continue // 仍在宽限期内
		}
		var n int64
		if err := s.DB.Model(&model.CronRun{}).
			Where("task_id = ? AND started_at >= ?", st.ID, due).Count(&n).Error; err != nil {
			continue
		}
		if n == 0 {
			out[st.ID] = StaleInfo{Name: st.Name, Due: due}
		}
	}
	return out
}

// watchdog 死开关巡检：对「预期已过宽限期仍未跑」的任务推送一次通知（同一预期时刻只推一次）。
func (s *Scheduler) watchdog(stale map[uint]StaleInfo) {
	if len(stale) == 0 {
		return
	}
	for id, info := range stale {
		s.alertMu.Lock()
		if s.staleAlerted == nil {
			s.staleAlerted = map[uint]time.Time{}
		}
		last, seen := s.staleAlerted[id]
		dup := seen && last.Equal(info.Due)
		if !dup {
			s.staleAlerted[id] = info.Due
		}
		s.alertMu.Unlock()
		if dup {
			continue
		}
		log.Printf("[cron] 死开关命中 id=%d name=%s 预期时刻=%s 未执行", id, info.Name, info.Due.Format(time.DateTime))
		notifyStale(info.Name, info.Due)
	}
}

// notifyFailure 任务失败时向 NotifyURL 推送文本通知（经 service/notify.SendText 发送，
// 按 URL 域名自动分派飞书/钉钉报文格式）。异步 best-effort：NotifyURL 读取与组文在
// 调用方协程完成，网络推送放 goroutine + defer recover（对齐 handler/alert_webhook.go
// 的 pushAlertNotify 风格；gin Recovery 不覆盖自起 goroutine，AGENTS 后端铁律第 10 条）。
// 原先在 execute 的 execMu 持锁内同步发送、最长拖 5s，现调度锁内只做取值，网络 IO 全在锁外。
// 任何失败只记日志，绝不影响任务执行结果与返回值；日志不打印 URL（可能内嵌机器人 token 等凭据）。
func notifyFailure(taskName, errMsg string) {
	// 启协程前取值：NotifyURL 是包级变量，协程内再读会与注入方存在并发读窗口
	url := NotifyURL
	if url == "" {
		return
	}
	text := fmt.Sprintf("[鸢航VirtKite] 计划任务失败: %s %s",
		taskName, truncateRunes(errMsg, notifyErrLimit))
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cron] 失败通知协程 panic task=%s: %v\n%s", taskName, r, debug.Stack())
			}
		}()
		if err := notify.SendText(url, text); err != nil {
			log.Printf("[cron] 失败通知推送失败 task=%s: %v", taskName, err)
			return
		}
		log.Printf("[cron] 失败通知已推送 task=%s", taskName)
	}()
}

// notifyStale 死开关告警推送（经 NotifyURL；为空则不推）。
func notifyStale(taskName string, due time.Time) {
	url := NotifyURL
	if url == "" {
		return
	}
	text := fmt.Sprintf("[鸢航VirtKite] 计划任务未按时执行: %s（预期 %s，已超过宽限期仍无执行记录，请检查调度器）",
		taskName, due.Format("2006-01-02 15:04:05"))
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cron] 死开关通知协程 panic task=%s: %v\n%s", taskName, r, debug.Stack())
			}
		}()
		if err := notify.SendText(url, text); err != nil {
			log.Printf("[cron] 死开关通知推送失败 task=%s: %v", taskName, err)
		}
	}()
}
