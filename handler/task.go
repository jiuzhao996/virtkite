package handler

import (
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/console"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/wsticket"
	"gorm.io/gorm"
)

// taskLookupError 任务查询失败的统一响应：记录不存在 → 404；数据库故障 → 500。
// 此前一律 404「任务不存在」——DB 不可用时用户会误以为任务被删，
// 排查方向被带偏（实际去回收站/审计里找，其实服务端连不上库）。
func taskLookupError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		Fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	ErrorWithMessage(c, http.StatusInternalServerError, "查询任务失败", err)
}

// TaskHandler 异步任务查询处理器（对应契约 GET/DELETE /api/tasks）。
type TaskHandler struct {
	DB    *gorm.DB
	Tasks *tasks.Manager
	// Tickets WS 一次性票据库（与 AuthMiddleware 校验侧同一实例，由 routes.go 装配）。
	// 供任务日志 WS 签发一次性短时票据（浏览器 WS 无法自定义请求头）。
	Tickets *wsticket.Store
}

// NewTaskHandler 创建任务查询处理器
func NewTaskHandler(db *gorm.DB, taskMgr *tasks.Manager) *TaskHandler {
	return &TaskHandler{DB: db, Tasks: taskMgr}
}

// ListTasks 任务列表 GET /api/tasks?status=&page=&page_size=（page 从 1 起，page_size 默认 50）。
// 返回 {total, items}，total 为筛选条件下的真实总数；兼容旧 limit 参数（等价 page_size）。
func (h *TaskHandler) ListTasks(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	status := c.Query("status")
	page, pageSize := parsePageQuery(c, 50, 200)
	items, total, err := h.Tasks.ListPaged(page, pageSize, status)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询任务列表失败", err)
		return
	}
	if items == nil {
		items = []model.Task{}
	}
	Success(c, gin.H{
		"total": total,
		"items": items,
	})
}

// GetTask 单个任务 GET /api/tasks/:id（含 result/error，不含 payload）。
func (h *TaskHandler) GetTask(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		Fail(c, http.StatusBadRequest, "任务 ID 不合法")
		return
	}
	task, err := h.Tasks.Get(uint(id))
	if err != nil {
		taskLookupError(c, err)
		return
	}
	Success(c, task)
}

// CancelTask POST /api/tasks/:id/cancel —— 用户取消运行中的任务（AU2）。
// 仅 running 可取消；不响应 ctx 的 executor（libvirt 类）底层操作可能继续跑完，
// 但任务状态以取消为准。幂等：非 running 返回「不在运行中」不算错误。
func (h *TaskHandler) CancelTask(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	cancelled, err := h.Tasks.Cancel(id)
	if err != nil {
		taskLookupError(c, err)
		return
	}
	if !cancelled {
		Fail(c, http.StatusConflict, "任务不在运行中，无法取消")
		return
	}
	Success(c, gin.H{"message": "已取消", "id": id})
}

// DeleteTask 删除任务 DELETE /api/tasks/:id，仅 finished（success/failed）可删。
func (h *TaskHandler) DeleteTask(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		Fail(c, http.StatusBadRequest, "任务 ID 不合法")
		return
	}
	task, err := h.Tasks.Get(uint(id))
	if err != nil {
		taskLookupError(c, err)
		return
	}
	// cancelled 也是终态：允许清理，否则取消过的任务永久占着列表
	// （UI 早已把 cancelled 当可删终态，后端此前只认 success/failed，两边矛盾）。
	if task.Status != model.TaskStatusSuccess && task.Status != model.TaskStatusFailed && task.Status != model.TaskStatusCancelled {
		Fail(c, http.StatusConflict, "仅允许删除已完成(success/failed/cancelled)的任务")
		return
	}
	if err := h.DB.Delete(&model.Task{}, task.ID).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "删除任务失败", err)
		return
	}
	Success(c, gin.H{"message": "任务已删除", "id": task.ID})
}

// ==================== 结构化日志（条目数组）与实时日志 WS ====================

// taskLogStatusPoll 日志 WS 的状态轮询周期：既用于「任务到终态主动关闭」，也用于补读
// DB 兜底漏帧（订阅缓冲溢出 / 日志器尚未创建）。2s 与日志落库节流同频。
const taskLogStatusPoll = 2 * time.Second

// taskWSResource 任务日志 WS 的票据资源标识。
// 必须与 middleware 里 wsTicketResource 对 /api/tasks/:id/logs/ws 的计算口径逐字一致，
// 否则 AuthMiddleware 的「票据与目标不匹配」会拦下所有连接（该 case 属接线项）。
func taskWSResource(id uint) string {
	return "task:" + strconv.FormatUint(uint64(id), 10)
}

// isTerminalTaskStatus 任务是否已到终态（终态后日志 WS 应主动关闭）。
func isTerminalTaskStatus(status string) bool {
	return status == model.TaskStatusSuccess || status == model.TaskStatusFailed || status == model.TaskStatusCancelled
}

// GetTaskLogs GET /api/tasks/:id/logs —— 返回任务的结构化执行日志（首屏全量）。
//
// 兼容判定（明确依据）：log_entries 非空且是 JSON 数组 → structured=true，entries 为结构化条目；
// 否则（老任务的 result 是纯文本/业务 JSON）→ structured=false，text 回传 result 供前端回退渲染。
func (h *TaskHandler) GetTaskLogs(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	task, err := h.Tasks.Get(id)
	if err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "任务不存在", err)
		return
	}
	entries, structured := tasks.ParseLogEntries(task.LogEntries)
	if entries == nil {
		entries = []model.TaskLogEntry{}
	}
	Success(c, gin.H{
		"structured": structured,
		"entries":    entries,
		"text":       task.Result,
	})
}

// MintLogWSTicket POST /api/tasks/:id/ws-ticket —— 为任务日志 WS 签发一次性短时票据。
// 与 VM/容器终端的 ws-ticket 同构：单次使用 + 30 秒有效 + 绑定到具体任务。
func (h *TaskHandler) MintLogWSTicket(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	if _, err := h.Tasks.Get(id); err != nil {
		Fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	if h.Tickets == nil {
		Fail(c, http.StatusServiceUnavailable, "连接凭证服务未就绪")
		return
	}
	tk, err := h.Tickets.Issue(c.GetUint("user_id"), taskWSResource(id))
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "签发连接凭证失败", err)
		return
	}
	Success(c, gin.H{"ticket": tk, "expires_in": int(wsticket.TTL.Seconds())})
}

// StreamLogs GET /api/tasks/:id/logs/ws?since=<seq> —— 任务日志实时增量流。
//
// 协议（服务端 → 客户端）：
//
//	{"type":"ready","task_id":N,"status":"..."}   连接就绪
//	{"type":"entries","entries":[{seq,ts,kind,text,hidden}, ...]}  增量批次（含首次补齐）
//	{"type":"done","status":"success|failed|cancelled"}  任务到终态，随后关闭
//	{"type":"error","msg":"中文文案"}  致命错误，随后关闭
//
// 增量语义：客户端首屏先 GET /logs 拿到全量并记下最大 seq，再带 ?since=<seq> 连本 WS；
// 服务端只回 seq>since 的条目，客户端按 seq 去重（GET 与 WS 可能重叠）。
//
// 为什么复用 console.Conn：本处理器有「主转发循环」与「读侧探活协程」两个写方
// （pong/心跳 + 条目推送），gorilla/websocket 禁止多 goroutine 并发写，必须串行化。
func (h *TaskHandler) StreamLogs(c *gin.Context) {
	rawConn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	conn := console.NewConn(rawConn)
	defer conn.Close()

	if h.Tasks == nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "任务系统未初始化"})
		return
	}
	id, ok := parseID(c.Param("id"))
	if !ok {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "任务 ID 非法"})
		return
	}
	task, err := h.Tasks.Get(id)
	if err != nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "任务不存在"})
		return
	}

	since, _ := strconv.Atoi(c.Query("since"))
	if since < 0 {
		since = 0
	}
	_ = conn.WriteJSON(gin.H{"type": "ready", "task_id": id, "status": task.Status})

	// 读侧探活协程：浏览器关闭/断网时 ReadMessage 立刻报错，据此通知主循环收工。
	// 自起协程必须自带 recover（项目规范 10）；读侧由本协程独占，写侧统一走 conn。
	clientGone := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[tasks] 日志 WS 读侧 panic task=%d panic=%v\n%s", id, r, debug.Stack())
			}
		}()
		for {
			if _, _, rerr := conn.ReadMessage(); rerr != nil {
				close(clientGone)
				return
			}
		}
	}()

	// 已到终态：回放 DB 快照后直接 done，无需订阅
	if isTerminalTaskStatus(task.Status) {
		sendLogEntries(conn, entriesSince(task.LogEntries, since))
		sendDone(conn, task.Status)
		return
	}

	// 订阅活日志器（若有），原子拿到 seq>since 的现存条目 + 增量通道（快照与订阅同一把锁，无空窗）
	var (
		ch    <-chan model.TaskLogEntry
		unsub = func() {}
	)
	if lg := h.Tasks.TaskLog(id); lg != nil {
		backlog, c2, u := lg.SubscribeSince(since)
		ch, unsub = c2, u
		sendLogEntries(conn, backlog)
		since = lastSeq(backlog, since)
	} else {
		// 任务尚未启动（pending）或日志器随进程重启丢失：回放 DB 权威快照兜底
		backlog := entriesSince(task.LogEntries, since)
		sendLogEntries(conn, backlog)
		since = lastSeq(backlog, since)
	}
	// 用闭包而非 `defer unsub()`：unsub 可能在下方 ticker 分支被重新赋值（补订阅），
	// `defer unsub()` 会在 defer 语句处就求值成旧函数值，导致新订阅永不退订而泄漏。
	defer func() { unsub() }()

	ticker := time.NewTicker(taskLogStatusPoll)
	defer ticker.Stop()
	for {
		select {
		case <-clientGone:
			return
		case e, ok := <-ch:
			if !ok {
				// 日志器关闭 = 任务到终态：补读一次 DB 尾部（覆盖关闭瞬间的最后一次落库）后收工
				if t, gerr := h.Tasks.Get(id); gerr == nil {
					sendLogEntries(conn, entriesSince(t.LogEntries, since))
					sendDone(conn, t.Status)
				}
				return
			}
			if e.Seq > since {
				sendLogEntries(conn, []model.TaskLogEntry{e})
				since = e.Seq
			}
		case <-ticker.C:
			// 尚未订阅到日志器（任务刚转 running）：补订阅
			if ch == nil {
				if lg := h.Tasks.TaskLog(id); lg != nil {
					backlog, c2, u := lg.SubscribeSince(since)
					ch, unsub = c2, u
					sendLogEntries(conn, backlog)
					since = lastSeq(backlog, since)
				}
			}
			// 周期重读 DB：覆盖订阅缓冲溢出丢帧与「日志器尚未创建」两种漏读，按 seq 去重
			t, gerr := h.Tasks.Get(id)
			if gerr != nil {
				return
			}
			if extra := entriesSince(t.LogEntries, since); len(extra) > 0 {
				sendLogEntries(conn, extra)
				since = lastSeq(extra, since)
			}
			if isTerminalTaskStatus(t.Status) {
				sendDone(conn, t.Status)
				return
			}
		}
	}
}

// sendLogEntries 发送一批日志条目（空批次不发，避免噪音帧）。
func sendLogEntries(conn *console.Conn, entries []model.TaskLogEntry) {
	if len(entries) == 0 {
		return
	}
	_ = conn.WriteJSON(gin.H{"type": "entries", "entries": entries})
}

// sendDone 发送终态帧（随后调用方关闭连接）。
func sendDone(conn *console.Conn, status string) {
	_ = conn.WriteJSON(gin.H{"type": "done", "status": status})
}

// entriesSince 从 log_entries 原文里解析并筛选 seq>since 的条目（解析失败返回 nil）。
func entriesSince(raw string, since int) []model.TaskLogEntry {
	entries, ok := tasks.ParseLogEntries(raw)
	if !ok {
		return nil
	}
	out := make([]model.TaskLogEntry, 0, len(entries))
	for _, e := range entries {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	return out
}

// lastSeq 返回条目里的最大 seq（不比 since 小），用于推进增量游标。
func lastSeq(entries []model.TaskLogEntry, since int) int {
	for _, e := range entries {
		if e.Seq > since {
			since = e.Seq
		}
	}
	return since
}
