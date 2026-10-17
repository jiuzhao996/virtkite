// 本文件负责 worker 执行循环与任务终态写入：取任务 → 置 running → 调度 executor
// （panic 兜底 + 执行级超时）→ 置 success/failed，含终态写库重试与错误文案抽取。
package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"time"
	"unicode"

	"github.com/jiuzhao/vmops/model"
)

// loop worker 主循环：从指定队列取 taskID 执行。队列永不关闭，worker 随进程退出。
func (m *Manager) loop(q chan uint) {
	for id := range q {
		m.run(id)
	}
}

// taskTimeout 返回该类型任务的执行上限（未登记走 defaultTaskTimeout）。
func taskTimeout(taskType string) time.Duration {
	if d, ok := taskTimeouts[taskType]; ok {
		return d
	}
	return defaultTaskTimeout
}

// run 执行单个任务：DB 读 task → 置 running → 查 executors → 执行 → 置 success/failed。
// executor 的 panic 由 runExecutor 兜住；这里的 defer 再兜一层（DB/JSON 等 executor 之外的裸奔点），
// 确保任何 panic 都不会冒泡到 loop——否则一个 panic 会直接带走整个进程。
func (m *Manager) run(id uint) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tasks] worker panic id=%d panic=%v\n%s", id, r, debug.Stack())
			m.markFailed(id, msgTaskPanic)
		}
	}()

	var task model.Task
	if err := m.DB.First(&task, id).Error; err != nil {
		log.Printf("[tasks] 读取任务失败 id=%d err=%v", id, err)
		return
	}
	task.Status = statusRunning
	if err := m.DB.Save(&task).Error; err != nil {
		log.Printf("[tasks] 置 running 失败 id=%d type=%s err=%v", id, task.Type, err)
		return
	}

	// 结构化日志器：所有任务统一挂一个（落库 tasks.log_entries + 实时推日志 WS）。
	// 刻意在 executor 之外创建：这样「未知类型/参数损坏」这类在 executor 之前就失败的任务
	// 也有日志可看；executor 通过 ctx.Log 写日志，无需各自实现攒批/脱敏/推送。
	lg := newTaskLogger(m.DB, id, m.secret(), m)
	defer lg.Close()
	lg.Info("任务开始执行")

	m.mu.RLock()
	fn, ok := m.executors[task.Type]
	m.mu.RUnlock()
	if !ok {
		// 终态写入失败会导致任务永久卡 running（全量审计 P1），走重试 + 留痕
		lg.Info("未知任务类型: " + task.Type)
		m.updateTaskFields(id, map[string]interface{}{
			"status": statusFailed,
			"error":  fmt.Sprintf("未知任务类型: %s", task.Type),
		}, "未知任务类型")
		return
	}

	payload := map[string]interface{}{}
	if len(task.Payload) > 0 {
		if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil {
			// 原始解析错误只进日志，DB 只存用户友好文案
			log.Printf("[tasks] 任务参数损坏 id=%d type=%s err=%v", id, task.Type, err)
			lg.Info("任务参数损坏，无法执行")
			m.updateTaskFields(id, map[string]interface{}{
				"status": statusFailed,
				"error":  "任务参数损坏，无法执行",
			}, "任务参数损坏")
			return
		}
	}

	// 用户取消（AU2）：ctx 传入 executor；Cancel 置状态后触发 CancelFunc
	ctx, cancel := context.WithCancel(context.Background())
	m.cancels.Store(id, cancel)
	defer m.cancels.Delete(id)
	execCtx := &ExecContext{
		DB:      m.DB,
		Virt:    m.Virt,
		Task:    &task,
		Payload: payload,
		Report:  m.reporter(id),
		Ctx:     ctx,
		Log:     lg,
		// 边界加密的 cloud-init 口令由 executor 解密；明文不落 tasks.payload
		MasterSecret: m.secret(),
	}
	// 执行级超时：executor 挂死（libvirt RPC 无响应 / 下载僵死 / 死循环）时，
	// 任务永久停在 running——worker 白白少一个，四角全卡死时整个任务系统瘫痪，
	// 且该 VM 的后续操作被 guardVMIdle 永久 409。到点置 failed 释放 worker。
	// 被超时的 executor goroutine 无法强杀（泄漏一个），但它已无人等待，后续
	// 任何 DB 写（Report 等）都无害，比永久占用 worker 好得多；真凶（挂住的
	// RPC）最终自行报错时写入 done（缓冲 1），goroutine 随之回收。
	timeout := taskTimeout(task.Type)
	done := make(chan error, 1)
	go func() { done <- runExecutor(fn, execCtx) }()
	timer := time.NewTimer(timeout)
	select {
	case err := <-done:
		timer.Stop()
		if m.isCancelled(id) {
			// 用户已取消：状态以取消为准，executor 的失败回写不覆盖
			return
		}
		if err != nil {
			// 原始错误链（含 libvirt 具体报错）只进日志，DB 只存 friendly 中文（该字段回显前端）
			log.Printf("[tasks] 任务失败 id=%d type=%s vm=%s err=%v", id, task.Type, task.VMName, err)
			lg.Info("任务执行失败: " + friendlyError(err))
			// executor 在失败路径设置的 Result（如删除任务失败时写明的已执行步骤与
			// 残留状态）一并落库：失败原因 Task.Error 只有脱敏短句，运维定位
			//「删到哪一步、能不能重试」靠这段上下文，不写就只剩翻日志一条路。
			if execCtx.Task != nil && execCtx.Task.Result != "" {
				m.updateTaskFields(id, map[string]interface{}{
					"status": statusFailed,
					"error":  friendlyError(err),
					"result": execCtx.Task.Result,
				}, "失败终态")
				return
			}
			m.markFailed(id, friendlyError(err))
			return
		}
	case <-timer.C:
		if m.isCancelled(id) {
			return
		}
		log.Printf("[tasks] !!! 任务执行超时 id=%d type=%s vm=%s timeout=%s", id, task.Type, task.VMName, timeout)
		lg.Info(fmt.Sprintf("任务执行超时（上限 %s）", timeout))
		m.markFailed(id, fmt.Sprintf("任务执行超时（上限 %s），已自动终止，请检查环境后重试", timeout))
		return
	}
	// 成功终态：写失败同样会造僵尸任务，必须走重试 + 留痕（旧实现 _ = 静默吞掉）
	if m.isCancelled(id) {
		return
	}
	lg.Info("任务执行成功")
	m.updateTaskFields(id, map[string]interface{}{
		"status":   statusSuccess,
		"progress": 100,
		"result":   execCtx.Task.Result,
		"vm_id":    execCtx.Task.VMID,
		"vm_name":  execCtx.Task.VMName,
	}, "成功终态")
}

// runExecutor 调度 executor 并兜住 panic：panic 时把 panic 值与完整堆栈
// （runtime/debug.Stack）打进日志，转成 errExecutorPanic 走统一失败路径，
// 堆栈与 panic 内容绝不写进 Task.Error（该字段会回显前端）。
func runExecutor(fn Executor, ctx *ExecContext) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tasks] executor panic type=%s id=%d panic=%v\n%s",
				ctx.Task.Type, ctx.Task.ID, r, debug.Stack())
			err = errExecutorPanic
		}
	}()
	return fn(ctx)
}

// updateTaskFields 按字段更新任务行，失败时有限重试（finalWriteAttempts 次、短退避）。
//
// 为什么不能像旧实现那样 `_ = m.DB...Updates(...)` 静默吞掉：终态写入是任务的唯一收口，
// 写失败等于任务永久停在 running——worker 已释放、HTTP 已返回，前端轮询无限转圈，
// 依赖「该 VM 无运行中任务」的后续操作（删除/克隆/导出）被永久阻塞，且全程无任何告警。
// 触发条件很常见：DB 瞬时抖动、连接池打满、行锁超时恰好发生在任务执行完毕那一刹那。
//
// 幂等性：Updates 按主键做等值字段写（非累加、非 CAS），重复执行结果一致，
// 重试不会让任务被结算两次。
//
// 不无限重试：累计退避约 300ms 即放弃，worker 继续取下一个任务；重试仍失败只留痕
// （醒目 ERROR 日志），不阻塞 worker，也不把已执行成功的任务翻成 failed 制造假失败——
// 卡住的僵尸任务由进程重启时的 sweepOrphanRunning 兜底清扫。
//
// scene 只用于日志区分场景（如「成功终态」「失败终态」），不参与 SQL。
// 不返回 error：失败已在内部按次数与最终结果全程留痕，返回去调用方也无处可补，
// 再让调用方处理只会引出一堆无人处理的返回值（或被 `_ =` 吞回老问题）。
func (m *Manager) updateTaskFields(id uint, values map[string]interface{}, scene string) {
	var lastErr error
	for attempt := 1; attempt <= finalWriteAttempts; attempt++ {
		lastErr = m.DB.Model(&model.Task{ID: id}).Updates(values).Error
		if lastErr == nil {
			if attempt > 1 {
				log.Printf("[tasks] 终态写入重试成功 id=%d 场景=%s 第 %d 次", id, scene, attempt)
			}
			return
		}
		log.Printf("[tasks] 终态写入失败 id=%d 场景=%s 第 %d/%d 次 err=%v",
			id, scene, attempt, finalWriteAttempts, lastErr)
		if attempt < finalWriteAttempts {
			time.Sleep(finalWriteBackoff * time.Duration(attempt))
		}
	}
	// 醒目留痕：任务将卡在非终态（前端无限转圈 + 后续写操作被阻塞），需运维介入
	log.Printf("[tasks] !!! 终态写入最终失败，任务将卡在 running id=%d 场景=%s err=%v", id, scene, lastErr)
}

// markFailed 按字段把任务置 failed（避免 Save 全行回写冲掉 Report 已上报的 progress）。
// 自带 recover：panic 兜底路径也走这里，DB 层再出意外也不能把进程带走。
// 写失败走 updateTaskFields 的重试与留痕，不再静默丢弃。
func (m *Manager) markFailed(id uint, msg string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tasks] 置 failed 时 panic id=%d panic=%v", id, r)
		}
	}()
	m.updateTaskFields(id, map[string]interface{}{
		"status": statusFailed,
		"error":  msg,
	}, "失败终态")
}

// reporter 返回写入指定任务进度的 Report 函数（msg 暂不持久化）。
//
// 进度回写是高频 best-effort：进度丢一两帧无后果，重试反而放大 DB 压力，故只写一次、
// 不重试；但绝不能像旧实现那样 `_ = ...Update(...)` 静默吞掉——DB 抖动时前端进度条会
// 停在某个百分比不动，排查时却没有任何线索。折中留痕：同一任务内首次失败打一条日志，
// 之后连续失败不再重复刷屏，写成功后复位（下次失败仍会留痕）。
//
// 不走 dbx.Persist 的原因：helper 每次失败必打日志且必重试，与这里「高频 + 只留首次」
// 的诉求冲突；语义不同的写入点各自选 helper 或直接留痕，见 service/dbx 包注释。
func (m *Manager) reporter(id uint) ProgressFunc {
	logged := false
	return func(pct int, msg string) {
		_ = msg
		if err := m.DB.Model(&model.Task{ID: id}).Update("progress", pct).Error; err != nil {
			if !logged {
				logged = true
				log.Printf("[tasks] 进度回写失败，前端进度条可能卡住 id=%d err=%v", id, err)
			}
			return
		}
		logged = false
	}
}

// friendlyError 从错误中提取用户友好中文（取 err.Error() 首段中文，截断 500）。
// 这里会丢掉原始错误链（无中文时统一降级成「操作失败」），因此调用方必须先把
// 完整错误打进日志（见 run），否则任务失败后无从排查。
func friendlyError(err error) string {
	if err == nil {
		return "操作失败"
	}
	msg := err.Error()
	if i := strings.IndexAny(msg, "：:"); i > 0 {
		msg = msg[:i]
	}
	if !containsCJK(msg) {
		return "操作失败"
	}
	return truncate(msg, maxErrorLen)
}

// containsCJK 判断字符串是否包含中日韩统一表意文字。
func containsCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// truncate 按 rune 截断字符串。
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
