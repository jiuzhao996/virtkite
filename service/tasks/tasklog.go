// tasklog.go：任务结构化日志（对应 Coolify 的结构化日志数组 + Semaphore 的双通道观感）。
//
// 旧实现把任务输出拼成一整段纯文本，2s 节流塞进 tasks.result（见 git 历史里的
// ansible_tasks.go / image_build.go）。本文件把它升级为「条目数组」并抽成可复用的
// TaskLogger：
//   - 每条 {seq, ts, kind, text, hidden}，落库进 tasks.log_entries（JSON 数组）；
//   - 保留 2s 节流批量落库（逐行 UPDATE 会打爆 DB）；
//   - 落库前对已知密钥明文做脱敏（见 logRedactor）；
//   - 同时向订阅者（日志 WS）实时推送，实现「增量推送」而不必等落库节流。
//
// 兼容性：老任务的 log_entries 为空、result 是纯文本——前端据 log_entries 是否存在
// 决定走「结构化渲染」还是「纯文本兜底」（见 handler.GetTaskLogs 的 structured 字段）。
//
// 条目解析与噪音折叠见 tasklog_parse.go，密钥脱敏见 tasklog_redact.go。
package tasks

import (
	"encoding/json"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
	"gorm.io/gorm"
)

const (
	// taskLogFlushInterval 日志落库节流间隔（沿用原 ansible/image_build 的 2s 攒批）。
	taskLogFlushInterval = 2 * time.Second
	// taskLogMaxEntries 内存与落库保留的最大条目数：超出丢最旧，防长任务（几小时构建）
	// 无限膨胀把内存与 mediumtext 列撑爆。丢掉的是最早的日志，尾部（失败现场）始终保留。
	taskLogMaxEntries = 4000
	// taskLogSubBuffer 单个订阅者（日志 WS）的发送缓冲。满了丢帧并留痕——DB 才是权威源，
	// 客户端凭 seq 重连即可补齐，绝不因一个慢客户端反压任务执行。
	taskLogSubBuffer = 512
)

// TaskLogger 单个任务的结构化日志器。
//
// 线程安全：executor 可能多 goroutine 并发调 Append（docker build 对 stdout/stderr 各起
// 一个读协程），故全部状态由 mu 保护。落库与推送在锁外进行，慢 DB / 慢客户端不阻塞 Append。
type TaskLogger struct {
	db     *gorm.DB
	taskID uint
	mgr    *Manager

	mu      sync.Mutex
	entries []model.TaskLogEntry
	seq     int
	dirty   bool // 有未落库的新条目
	closed  bool
	subs    map[chan model.TaskLogEntry]struct{}
	lagged  bool // 是否已因订阅缓冲溢出丢过帧（只留痕一次）

	writeMu sync.Mutex // 串行化落库写，避免 flushLoop 与 Close 并发写同一列
	stop    chan struct{}
	stopped sync.Once

	redactor *logRedactor
}

// newTaskLogger 创建并登记一个任务日志器：载入既有条目续接 seq，起 2s 节流落库协程。
func newTaskLogger(db *gorm.DB, taskID uint, masterSecret string, mgr *Manager) *TaskLogger {
	lg := &TaskLogger{
		db:       db,
		taskID:   taskID,
		mgr:      mgr,
		subs:     make(map[chan model.TaskLogEntry]struct{}),
		stop:     make(chan struct{}),
		redactor: newLogRedactor(db, taskID, masterSecret),
	}
	lg.loadExisting()
	if mgr != nil {
		mgr.registerLogger(taskID, lg)
	}
	go lg.flushLoop()
	return lg
}

// loadExisting 载入该任务已落库的日志（重试/续跑场景），保证 seq 单调、快照完整。
func (lg *TaskLogger) loadExisting() {
	if lg.db == nil {
		return
	}
	var t model.Task
	if err := lg.db.Select("log_entries").First(&t, lg.taskID).Error; err != nil {
		return
	}
	entries, ok := ParseLogEntries(t.LogEntries)
	if !ok || len(entries) == 0 {
		return
	}
	lg.entries = entries
	lg.seq = entries[len(entries)-1].Seq
}

// flushLoop 2s 节流落库协程。自起协程必须自带 recover（项目规范 10），panic 只进日志。
func (lg *TaskLogger) flushLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tasks] 日志落库协程 panic task=%d panic=%v\n%s", lg.taskID, r, debug.Stack())
		}
	}()
	ticker := time.NewTicker(taskLogFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			lg.flush()
		case <-lg.stop:
			return
		}
	}
}

// Append 追加一条可见日志（kind 取 model.TaskLogKind*）。
func (lg *TaskLogger) Append(kind, text string) { lg.append(kind, text, false) }

// Info 追加一条说明性条目（任务阶段提示等）。
func (lg *TaskLogger) Info(text string) { lg.append(model.TaskLogKindInfo, text, false) }

// Command 追加一条「即将执行的命令」条目（前端高亮）。
func (lg *TaskLogger) Command(text string) { lg.append(model.TaskLogKindCommand, text, false) }

// Debug 追加一条调试条目（hidden=true，前端默认折叠）。
func (lg *TaskLogger) Debug(text string) { lg.append(model.TaskLogKindStdout, text, true) }

// AppendLine 处理未区分 stdout/stderr 的合并流单行输出（ansible/docker build 的 OnLine），
// 按噪声启发式决定是否折叠为调试行。
func (lg *TaskLogger) AppendLine(text string) {
	lg.append(model.TaskLogKindStdout, text, isNoiseLogLine(text))
}

// append 追加条目：脱敏 → 分配 seq → 入内存 → 推送订阅者（推送在锁外，慢客户端不阻塞执行）。
func (lg *TaskLogger) append(kind, text string, hidden bool) {
	// 落库前脱敏：内存条目与 WS 推送都拿到脱敏后的文本，绝不让密钥明文出进程
	text = lg.redactor.redact(text)

	lg.mu.Lock()
	if lg.closed {
		lg.mu.Unlock()
		return
	}
	lg.seq++
	e := model.TaskLogEntry{
		Seq:    lg.seq,
		Ts:     time.Now().UnixMilli(),
		Kind:   kind,
		Text:   text,
		Hidden: hidden,
	}
	lg.entries = append(lg.entries, e)
	if len(lg.entries) > taskLogMaxEntries {
		lg.entries = lg.entries[len(lg.entries)-taskLogMaxEntries:]
	}
	lg.dirty = true
	subs := make([]chan model.TaskLogEntry, 0, len(lg.subs))
	for ch := range lg.subs {
		subs = append(subs, ch)
	}
	lg.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default:
			lg.noteLag()
		}
	}
}

// noteLag 记录一次「订阅缓冲溢出丢帧」——只打一次日志，避免刷屏。
func (lg *TaskLogger) noteLag() {
	lg.mu.Lock()
	first := !lg.lagged
	lg.lagged = true
	lg.mu.Unlock()
	if first {
		log.Printf("[tasks] 日志 WS 订阅缓冲已满，丢弃部分实时帧 task=%d（客户端凭 seq 重连可补齐）", lg.taskID)
	}
}

// flush 把当前条目落库（节流与终态各调一次）。写失败用 dbx 留痕并复位 dirty 以便下次重试。
func (lg *TaskLogger) flush() {
	lg.mu.Lock()
	if !lg.dirty {
		lg.mu.Unlock()
		return
	}
	lg.dirty = false
	data, err := json.Marshal(lg.entries)
	lg.mu.Unlock()
	if err != nil {
		log.Printf("[tasks] 任务日志序列化失败 task=%d err=%v", lg.taskID, err)
		return
	}
	lg.writeMu.Lock()
	werr := dbx.Persist(lg.db, "任务日志落库", func() error {
		return lg.db.Model(&model.Task{}).Where("id = ?", lg.taskID).
			Update("log_entries", string(data)).Error
	})
	lg.writeMu.Unlock()
	if werr != nil {
		// 落库失败：复位 dirty，下一拍（或 Close）再试；内存条目仍在，前端 WS 仍能实时看到
		lg.mu.Lock()
		lg.dirty = true
		lg.mu.Unlock()
	}
}

// SubscribeSince 原子地返回 seq>since 的现存条目与后续增量的接收通道。
//
// 「快照 + 订阅」在同一把锁内完成，杜绝「读快照」与「订阅」之间新增条目丢失的空窗；
// 订阅者另按 seq 去重（客户端首屏 GET 与这里可能重叠，seq 保证幂等）。
func (lg *TaskLogger) SubscribeSince(since int) (backlog []model.TaskLogEntry, ch <-chan model.TaskLogEntry, unsub func()) {
	lg.mu.Lock()
	defer lg.mu.Unlock()
	for _, e := range lg.entries {
		if e.Seq > since {
			backlog = append(backlog, e)
		}
	}
	if lg.closed {
		return backlog, nil, func() {}
	}
	c := make(chan model.TaskLogEntry, taskLogSubBuffer)
	lg.subs[c] = struct{}{}
	return backlog, c, func() {
		lg.mu.Lock()
		defer lg.mu.Unlock()
		if _, ok := lg.subs[c]; ok {
			delete(lg.subs, c)
			close(c)
		}
	}
}

// EntriesSince 返回 seq>since 的现存条目（不订阅）。供不需要实时增量的场景使用。
func (lg *TaskLogger) EntriesSince(since int) []model.TaskLogEntry {
	lg.mu.Lock()
	defer lg.mu.Unlock()
	var out []model.TaskLogEntry
	for _, e := range lg.entries {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	return out
}

// Close 收口：停节流协程 → 标记关闭并关闭订阅通道（唤醒 WS 转发循环）→ 终态刷一次 → 注销登记。
// 幂等，可安全被 run 的 defer 与 executor 各调一次。
func (lg *TaskLogger) Close() {
	lg.stopped.Do(func() { close(lg.stop) })

	lg.mu.Lock()
	if lg.closed {
		lg.mu.Unlock()
		return
	}
	lg.closed = true
	subs := make([]chan model.TaskLogEntry, 0, len(lg.subs))
	for c := range lg.subs {
		subs = append(subs, c)
	}
	lg.subs = make(map[chan model.TaskLogEntry]struct{})
	lg.mu.Unlock()

	for _, c := range subs {
		close(c)
	}
	lg.flush() // 终态刷一次：最后不足一个节流窗口的日志不能丢
	if lg.mgr != nil {
		lg.mgr.unregisterLogger(lg.taskID, lg)
	}
}
