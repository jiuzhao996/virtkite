// Package tasks 提供异步任务队列（对应 JumpServer/PVE task 队列语义）。
//
// Manager 内部维护固定 worker 池：Submit 只负责任务入库与入队，
// worker goroutine 负责取任务、置 running、调度 Executor、回写 success/failed。
package tasks

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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
	// defaultListLimit List 默认条数。
	defaultListLimit = 50
	// maxListLimit List 条数上限。
	maxListLimit = 200
	// maxErrorLen Task.Error 最大长度（截断，单位 rune）。
	maxErrorLen = 500
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
type Manager struct {
	DB        *gorm.DB
	Virt      *virt.Virt
	queue     chan uint // taskID 队列，worker 只读
	quit      chan struct{}
	executors map[string]Executor
	mu        sync.RWMutex
}

// NewManager 创建任务管理器：virt.New() 惰性连接，起 4 个 worker goroutine。
// worker 只读 queue 与 quit，进程退出即弃，无需优雅关闭。
func NewManager(db *gorm.DB) *Manager {
	m := &Manager{
		DB:        db,
		Virt:      virt.New(),
		queue:     make(chan uint, queueBufferSize),
		quit:      make(chan struct{}),
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
// 入队非阻塞：queue 满则起独立 goroutine 阻塞入队，保证 Submit 绝不阻塞 handler。
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

	select {
	case m.queue <- task.ID:
	default:
		go func() { m.queue <- task.ID }()
	}
	return task, nil
}

// Get 查询单个任务。
func (m *Manager) Get(id uint) (*model.Task, error) {
	var task model.Task
	if err := m.DB.First(&task, id).Error; err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	return &task, nil
}

// List 按 id 倒序查询任务列表，status 为空查全部。
func (m *Manager) List(limit int, status string) ([]model.Task, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	items := []model.Task{}
	q := m.DB.Order("id desc").Limit(limit)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Find(&items).Error; err != nil {
		return nil, fmt.Errorf("查询任务列表失败: %w", err)
	}
	return items, nil
}

// loop worker 主循环：取 taskID 执行，收到 quit 则退出。
func (m *Manager) loop() {
	for {
		select {
		case id := <-m.queue:
			m.run(id)
		case <-m.quit:
			return
		}
	}
}

// run 执行单个任务：DB 读 task → 置 running → 查 executors → 执行 → 置 success/failed。
func (m *Manager) run(id uint) {
	var task model.Task
	if err := m.DB.First(&task, id).Error; err != nil {
		return
	}
	task.Status = statusRunning
	if err := m.DB.Save(&task).Error; err != nil {
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
	if err := fn(execCtx); err != nil {
		// 按字段更新，避免 Save 全行回写冲掉 Report 已上报的 progress
		_ = m.DB.Model(&model.Task{ID: id}).Updates(map[string]interface{}{
			"status": statusFailed,
			"error":  friendlyError(err),
		})
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

// reporter 返回写入指定任务进度的 Report 函数（msg 暂不持久化）。
func (m *Manager) reporter(id uint) ProgressFunc {
	return func(pct int, msg string) {
		_ = msg
		_ = m.DB.Model(&model.Task{ID: id}).Update("progress", pct)
	}
}

// friendlyError 从错误中提取用户友好中文（取 err.Error() 首段中文，截断 500）。
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
