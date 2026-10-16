// Package tasks 提供异步任务队列（对应 JumpServer/PVE task 队列语义）。
//
// Manager 内部维护固定 worker 池：Submit 只负责任务入库与入队，
// worker goroutine 负责取任务、置 running、调度 Executor、回写 success/failed。
// worker 内的 panic 一律被 recover 兜住：只失败当前任务，worker 继续取下一个，进程不退出
// （gin 的 Recovery 中间件只覆盖 HTTP 请求链，不覆盖后台 worker goroutine）。
//
// 本文件是 Manager 的核心定义与生命周期/提交/查询接口；常量与分池/超时配置见
// manager_config.go，worker 执行循环与终态写入见 manager_run.go。
package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
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
	// Ctx 取消信号（AU2）：用户取消时触发；不响应它的 executor 也能跑完，
	// 但任务状态以 cancelled 为准。nil 时按 context.Background() 使用。
	Ctx context.Context
	// Log 结构化任务日志器（worker 创建，落库进 tasks.log_entries 并实时推给日志 WS）。
	// executor 通过它写日志：Log.Info / Log.Command / Log.AppendLine / Log.Debug。
	// 可为 nil（如直接构造 ExecContext 的单测），调用方需容错。
	Log *TaskLogger
}

// Executor 任务执行函数：返回 nil=成功（可写 ctx.Task.Result），error=失败（中文友好，写 Task.Error）。
type Executor func(ctx *ExecContext) error

// Manager 异步任务队列管理器（对应 JumpServer/PVE task 队列语义）。
// 快慢双池：秒级任务（电源操作）与分钟级任务（建删/克隆/下载）分队列分 worker，
// 长任务排满慢池也不影响开关机的响应性。
// Manager 生命周期与进程一致，不提供单独关闭入口（无 quit/Close，进程退出即弃）。
type Manager struct {
	DB        *gorm.DB
	Virt      *virt.Virt
	queueFast chan uint // 快池 taskID 队列（秒级任务）
	queueSlow chan uint // 慢池 taskID 队列（分钟级任务，见 slowTaskTypes）
	executors map[string]Executor
	mu        sync.RWMutex
	// cancels 运行中任务的取消函数表（taskID → context.CancelFunc），AU2 用户取消。
	cancels sync.Map
	// masterSecret 凭据主密钥（由 Register* 注入，与 handler.CredentialMasterSecret() 同源）。
	// 用于构建任务日志脱敏器：解密本任务相关 VM 的托管凭据以纳入脱敏集合。
	masterSecret string
	// loggers 运行中任务的日志器登记表（taskID → *TaskLogger），供日志 WS 查找实时增量源。
	// 用普通 map + 锁（而非 sync.Map）：注销时要按「值相等」判定，避免把重试后的新日志器误删。
	logMu   sync.Mutex
	loggers map[uint]*TaskLogger
}

// NewManager 创建任务管理器：virt.New() 惰性连接，起快慢两组 worker goroutine 与重试扫描协程。
// worker 只读各自队列，随进程一起退出，不做优雅关闭。
func NewManager(db *gorm.DB) *Manager {
	m := &Manager{
		DB:        db,
		Virt:      virt.New(),
		queueFast: make(chan uint, queueBufferSize),
		queueSlow: make(chan uint, queueBufferSize),
		executors: map[string]Executor{},
		loggers:   map[uint]*TaskLogger{},
	}
	for i := 0; i < fastWorkerCount; i++ {
		go m.loop(m.queueFast)
	}
	for i := 0; i < slowWorkerCount; i++ {
		go m.loop(m.queueSlow)
	}
	m.sweepOrphanRunning()
	m.StartRetryScanner()
	return m
}

// SetMasterSecret 注入凭据主密钥（Register* 调用时传入）。空串表示未配置，脱敏跳过凭据解密。
func (m *Manager) SetMasterSecret(secret string) {
	m.mu.Lock()
	m.masterSecret = secret
	m.mu.Unlock()
}

// secret 读凭据主密钥（worker 构建日志脱敏器时用）。
func (m *Manager) secret() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.masterSecret
}

// registerLogger 登记运行中任务的日志器（日志 WS 据此找到实时增量源）。
// 容忍 loggers 未初始化（手工构造 Manager 的单测不走 NewManager）——懒建 map。
func (m *Manager) registerLogger(taskID uint, lg *TaskLogger) {
	m.logMu.Lock()
	if m.loggers == nil {
		m.loggers = map[uint]*TaskLogger{}
	}
	m.loggers[taskID] = lg
	m.logMu.Unlock()
}

// unregisterLogger 注销日志器：仅当登记的正是该实例时才删（重试后新日志器可能已覆盖）。
func (m *Manager) unregisterLogger(taskID uint, lg *TaskLogger) {
	m.logMu.Lock()
	if cur, ok := m.loggers[taskID]; ok && cur == lg {
		delete(m.loggers, taskID)
	}
	m.logMu.Unlock()
}

// TaskLog 返回该任务当前运行中的日志器（不存在返回 nil：任务未启动/已结束/进程重启）。
func (m *Manager) TaskLog(taskID uint) *TaskLogger {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	return m.loggers[taskID]
}

// sweepOrphanRunning 启动时把上一进程遗留的 running/pending 任务置为 failed：
// 任务队列只在内存中（channel），进程重启后这些行永远不会被执行，不扫就是僵尸任务。
// 同时把 retry_state 置 aborted：被中断的任务不参与自动重试（重试只针对「执行完并失败」的任务，
// 进程重启中断的语义完全不同，自动重投可能落在半完成的状态上）。
func (m *Manager) sweepOrphanRunning() {
	res := m.DB.Model(&model.Task{}).
		Where("status IN ?", []string{statusRunning, statusPending}).
		Updates(map[string]interface{}{
			"status":      statusFailed,
			"error":       "服务重启，任务已中断",
			"retry_state": retryStateAborted,
		})
	if res.Error != nil {
		log.Printf("[tasks] 清扫遗留任务失败 err=%v", res.Error)
		return
	}
	if res.RowsAffected > 0 {
		log.Printf("[tasks] 已清扫上次进程遗留任务 %d 个（running/pending → failed）", res.RowsAffected)
	}
}

// Register 注册任务类型的执行函数（启动时调用，worker 只读）。
func (m *Manager) Register(taskType string, fn Executor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executors[taskType] = fn
}

// Submit 提交任务：payload 序列化 JSON 存 Payload，Status=pending 入库后入队。
// 入队有界（见 enqueue）：队列满时最多等 enqueueTimeout，超时把任务置 failed 并
// 返回 ErrQueueBusy（不再假受理），既不为入队起无上限 goroutine，也不静默丢任务。
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
		// MaxRetry 按重试白名单在提交时固化：只有幂等安全的类型才 > 0，重试扫描器据此重投
		//（见 retry.go 的 retryableTypes 取舍）。非白名单类型恒为 0 = 永不自动重试。
		MaxRetry: maxRetryFor(taskType),
	}
	if err := m.DB.Create(task).Error; err != nil {
		return nil, fmt.Errorf("创建任务记录失败: %w", err)
	}

	if err := m.enqueue(task); err != nil {
		return nil, err
	}
	return task, nil
}

// enqueue 有界入队：按任务类型选池（见 slowTaskTypes），先非阻塞尝试；队列满则用
// 单个 Timer 最多等 enqueueTimeout（只 NewTimer 一次并 Stop，不在循环里反复 time.After）。
// 等满仍入不了说明积压严重：置 failed 留痕并返回 ErrQueueBusy——旧实现返回成功，
// handler 照常回 202，前端拿着注定 failed 的 task_id 轮询才知失败（假受理）。
//
// 这里选择「调用方同步等待」而非「限量后台 goroutine」：等待发生在 handler 自己的
// 请求 goroutine 上（本就存在、由连接数天然限流），goroutine 数量零增长，
// 且能把背压如实传回客户端；代价是队列满时 Submit 最多阻塞 enqueueTimeout。
func (m *Manager) enqueue(task *model.Task) error {
	q := m.queueFor(task.Type)
	select {
	case q <- task.ID:
		return nil
	default:
	}

	timer := time.NewTimer(enqueueTimeout)
	defer timer.Stop()
	select {
	case q <- task.ID:
		return nil
	case <-timer.C:
		log.Printf("[tasks] 入队超时（队列已满）id=%d type=%s vm=%s wait=%s",
			task.ID, task.Type, task.VMName, enqueueTimeout)
		// DB 记录留痕（任务列表可见失败原因），错误如实返回给 handler
		m.markFailed(task.ID, msgQueueBusy)
		return ErrQueueBusy
	}
}

// queueFor 按任务类型返回目标队列（慢池见 slowTaskTypes 登记表）。
func (m *Manager) queueFor(taskType string) chan uint {
	if slowTaskTypes[taskType] {
		return m.queueSlow
	}
	return m.queueFast
}

// Cancel 用户取消运行中的任务（AU2）。cancelled=false 表示不在运行态（幂等，不算错误）。
func (m *Manager) Cancel(id uint) (cancelled bool, err error) {
	var task model.Task
	if err := m.DB.First(&task, id).Error; err != nil {
		return false, err
	}
	if task.Status != statusRunning {
		return false, nil
	}
	m.updateTaskFields(id, map[string]interface{}{
		"status": statusCancelled,
		"error":  "已被用户取消",
	}, "用户取消")
	if c, ok := m.cancels.Load(id); ok {
		if cf, ok2 := c.(context.CancelFunc); ok2 {
			cf()
		}
	}
	log.Printf("[tasks] 任务已取消 id=%d type=%s", id, task.Type)
	return true, nil
}

// isCancelled 任务当前是否已被用户取消（终态回写防覆盖用）。
func (m *Manager) isCancelled(id uint) bool {
	var status string
	if err := m.DB.Model(&model.Task{}).Where("id = ?", id).Pluck("status", &status).Error; err != nil {
		return false
	}
	return status == statusCancelled
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
