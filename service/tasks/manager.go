// Package tasks 提供异步任务队列（对应 JumpServer/PVE task 队列语义）。
//
// Manager 内部维护固定 worker 池：Submit 只负责任务入库与入队，
// worker goroutine 负责取任务、置 running、调度 Executor、回写 success/failed。
// worker 内的 panic 一律被 recover 兜住：只失败当前任务，worker 继续取下一个，进程不退出
// （gin 的 Recovery 中间件只覆盖 HTTP 请求链，不覆盖后台 worker goroutine）。
package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

const (
	// workerCount worker goroutine 数量（固定 worker 池）。
	workerCount = 4
	// queueBufferSize 任务队列缓冲长度。
	queueBufferSize = 128
	// enqueueTimeout 队列满时的入队等待上限，超时把任务置 failed
	// （宁可失败并告知用户，也不为入队起无上限 goroutine 悬挂等待）。
	enqueueTimeout = 3 * time.Second
	// defaultListLimit List 默认条数。
	defaultListLimit = 50
	// maxListLimit List 条数上限。
	maxListLimit = 200
	// maxErrorLen Task.Error 最大长度（截断，单位 rune）。
	maxErrorLen = 500
)

// WorkerCount / QueueBufferSize 供系统设置页展示真实生效值（handler/settings 快照读取）。
const (
	WorkerCount     = workerCount
	QueueBufferSize = queueBufferSize
)

const (
	// statusPending 任务等待执行。
	statusPending = "pending"
	// statusRunning 任务执行中。
	statusRunning = "running"
	// statusSuccess 任务执行成功。
	statusSuccess = "success"
	// statusFailed 任务执行失败。
	statusFailed = "failed"
)

const (
	// msgTaskPanic 任务 panic 后的用户可见文案（panic 值与堆栈只进日志，不入库）。
	msgTaskPanic = "任务执行异常，请查看服务日志"
	// msgQueueBusy 入队超时的用户可见文案（队列积压，任务未能入队执行）。
	msgQueueBusy = "任务队列繁忙，请稍后重试"
)

// errExecutorPanic executor panic 转换出的错误：走统一失败路径，
// friendlyError 会原样透出 msgTaskPanic（不含冒号且含中文）。
var errExecutorPanic = errors.New(msgTaskPanic)

// ProgressFunc 上报任务进度（内部写 DB task.Progress）。
type ProgressFunc func(pct int, msg string)

// ExecContext 任务执行上下文，Executor 通过它访问 DB/Virt/参数与进度上报。
type ExecContext struct {
	DB      *gorm.DB
	Virt    *virt.Virt
	Task    *model.Task            // DB 记录（worker 内更新 Status/Progress/Result/Error）
	Payload map[string]interface{} // task.Payload 反序列化
	Report  ProgressFunc           // 上报进度（内部写 DB task.Progress）
}

// Executor 任务执行函数：返回 nil=成功（可写 ctx.Task.Result），error=失败（中文友好，写 Task.Error）。
type Executor func(ctx *ExecContext) error

// Manager 异步任务队列管理器（对应 JumpServer/PVE task 队列语义）。
// Manager 生命周期与进程一致，不提供单独关闭入口（无 quit/Close，进程退出即弃）。
type Manager struct {
	DB        *gorm.DB
	Virt      *virt.Virt
	queue     chan uint // taskID 队列，worker 只读
	executors map[string]Executor
	mu        sync.RWMutex
}

// NewManager 创建任务管理器：virt.New() 惰性连接，起 4 个 worker goroutine。
// worker 只读 queue，随进程一起退出，不做优雅关闭。
func NewManager(db *gorm.DB) *Manager {
	m := &Manager{
		DB:        db,
		Virt:      virt.New(),
		queue:     make(chan uint, queueBufferSize),
		executors: map[string]Executor{},
	}
	for i := 0; i < workerCount; i++ {
		go m.loop()
	}
	return m
}

// Register 注册任务类型的执行函数（启动时调用，worker 只读）。
func (m *Manager) Register(taskType string, fn Executor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executors[taskType] = fn
}

// Submit 提交任务：payload 序列化 JSON 存 Payload，Status=pending 入库后入队。
// 入队有界（见 enqueue）：队列满时最多等 enqueueTimeout，超时把任务置 failed 并返回，
// 既不为入队起无上限 goroutine，也不静默丢任务。
func (m *Manager) Submit(
	taskType string,
	title string,
	payload interface{},
	userID *uint,
	username string,
	vmName string,
	vmID *uint,
) (*model.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化任务参数失败: %w", err)
	}

	task := &model.Task{
		Type:     taskType,
		Title:    title,
		Status:   statusPending,
		Payload:  string(data),
		UserID:   userID,
		Username: username,
		VMID:     vmID,
		VMName:   vmName,
	}
	if err := m.DB.Create(task).Error; err != nil {
		return nil, fmt.Errorf("创建任务记录失败: %w", err)
	}

	m.enqueue(task)
	return task, nil
}

// enqueue 有界入队：先非阻塞尝试；队列满则用单个 Timer 最多等 enqueueTimeout
// （只 NewTimer 一次并 Stop，不在循环里反复 time.After）。等满仍入不了说明积压严重，
// 直接把任务置 failed 并打日志——旧实现 `go func(){ m.queue <- id }()` 会随提交频率
// 堆积无上限且永不退出的阻塞 goroutine。
//
// 这里选择「调用方同步等待」而非「限量后台 goroutine」：等待发生在 handler 自己的
// 请求 goroutine 上（本就存在、由连接数天然限流），goroutine 数量零增长，
// 且能把背压如实传回客户端；代价是队列满时 Submit 最多阻塞 enqueueTimeout。
func (m *Manager) enqueue(task *model.Task) {
	select {
	case m.queue <- task.ID:
		return
	default:
	}

	timer := time.NewTimer(enqueueTimeout)
	defer timer.Stop()
	select {
	case m.queue <- task.ID:
	case <-timer.C:
		log.Printf("[tasks] 入队超时（队列已满）id=%d type=%s vm=%s wait=%s",
			task.ID, task.Type, task.VMName, enqueueTimeout)
		// 同步回写返回给 handler 的 task，避免前端拿到 pending 却永远等不到执行
		task.Status = statusFailed
		task.Error = msgQueueBusy
		m.markFailed(task.ID, msgQueueBusy)
	}
}

// Get 查询单个任务。
func (m *Manager) Get(id uint) (*model.Task, error) {
	var task model.Task
	if err := m.DB.First(&task, id).Error; err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	return &task, nil
}

// ListPaged 分页版任务列表：返回当前页与筛选条件下的真实总数。
// page 从 1 起；pageSize 钳制到 [1, maxListLimit]。
func (m *Manager) ListPaged(page, pageSize int, status string) ([]model.Task, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultListLimit
	}
	if pageSize > maxListLimit {
		pageSize = maxListLimit
	}

	q := m.DB.Model(&model.Task{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计任务总数失败: %w", err)
	}

	items := []model.Task{}
	if err := q.Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("查询任务列表失败: %w", err)
	}
	return items, total, nil
}

// loop worker 主循环：从队列取 taskID 执行。queue 永不关闭，worker 随进程退出。
func (m *Manager) loop() {
	for id := range m.queue {
		m.run(id)
	}
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

	m.mu.RLock()
	fn, ok := m.executors[task.Type]
	m.mu.RUnlock()
	if !ok {
		task.Status = statusFailed
		task.Error = fmt.Sprintf("未知任务类型: %s", task.Type)
		_ = m.DB.Save(&task)
		return
	}

	payload := map[string]interface{}{}
	if len(task.Payload) > 0 {
		if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil {
			// 原始解析错误只进日志，DB 只存用户友好文案
			log.Printf("[tasks] 任务参数损坏 id=%d type=%s err=%v", id, task.Type, err)
			task.Status = statusFailed
			task.Error = "任务参数损坏，无法执行"
			_ = m.DB.Save(&task)
			return
		}
	}

	execCtx := &ExecContext{
		DB:      m.DB,
		Virt:    m.Virt,
		Task:    &task,
		Payload: payload,
		Report:  m.reporter(id),
	}
	if err := runExecutor(fn, execCtx); err != nil {
		// 原始错误链（含 libvirt 具体报错）只进日志，DB 只存 friendly 中文（该字段回显前端）
		log.Printf("[tasks] 任务失败 id=%d type=%s vm=%s err=%v", id, task.Type, task.VMName, err)
		m.markFailed(id, friendlyError(err))
		return
	}
	_ = m.DB.Model(&model.Task{ID: id}).Updates(map[string]interface{}{
		"status":   statusSuccess,
		"progress": 100,
		"result":   execCtx.Task.Result,
		"vm_id":    execCtx.Task.VMID,
		"vm_name":  execCtx.Task.VMName,
	})
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

// markFailed 按字段把任务置 failed（避免 Save 全行回写冲掉 Report 已上报的 progress）。
// 自带 recover：panic 兜底路径也走这里，DB 层再出意外也不能把进程带走。
func (m *Manager) markFailed(id uint, msg string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tasks] 置 failed 时 panic id=%d panic=%v", id, r)
		}
	}()
	_ = m.DB.Model(&model.Task{ID: id}).Updates(map[string]interface{}{
		"status": statusFailed,
		"error":  msg,
	})
}

// reporter 返回写入指定任务进度的 Report 函数（msg 暂不持久化）。
func (m *Manager) reporter(id uint) ProgressFunc {
	return func(pct int, msg string) {
		_ = msg
		_ = m.DB.Model(&model.Task{ID: id}).Update("progress", pct)
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
