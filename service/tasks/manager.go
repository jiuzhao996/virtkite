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
	// fastWorkerCount 快池 worker 数：只跑秒级 libvirt RPC 型任务（开关机），
	// 保证电源操作永远有 worker 响应，不被长任务排队挤占。
	fastWorkerCount = 4
	// slowWorkerCount 慢池 worker 数：跑分钟级 IO/网络型任务（建删盘、克隆、
	// 下载、playbook）。限 2 个并发：这类任务瓶颈在磁盘/带宽，并发再高只是
	// 相互拖慢 + 把宿主机 IO 打满。
	slowWorkerCount = 2
	// queueBufferSize 任务队列缓冲长度（快慢两池各自一条，互不占用）。
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
	// finalWriteAttempts 终态写入（success/failed）的最大尝试次数。
	finalWriteAttempts = 3
	// finalWriteBackoff 终态写入重试的退避基数：第 n 次失败后睡 n*基数，
	// 三次最多累计 300ms——worker 循环不会被一次 DB 抖动拖垮。
	finalWriteBackoff = 100 * time.Millisecond
)

// WorkerCountFast / WorkerCountSlow / QueueBufferSize 供系统设置页展示真实生效值
// （handler/settings 快照读取）。
const (
	WorkerCountFast = fastWorkerCount
	WorkerCountSlow = slowWorkerCount
	QueueBufferSize = queueBufferSize
)

// slowTaskTypes 分池表：慢池（IO/网络型，分钟级）任务类型；不在表内走快池。
// 分界依据是「任务时长的量级」，不是重要性——快池存在的意义只有一个：
// 电源操作这类秒级任务不排队。新任务类型默认进快池，确属长任务的必须显式登记，
// 否则一个长任务就能让开关机排 10 分钟队。
var slowTaskTypes = map[string]bool{
	"create_vm":       true, // 建卷 + cloud-init + 可选首启
	"delete_vm":       true, // undefine + 逐卷删除（卷多时 IO 慢）
	"clone_vm":        true, // 全盘拷贝
	"clone_image_vm":  true, // 镜像克隆建机
	"cleanup_volumes": true, // 孤儿卷枚举 + 批量删
	"image_download":  true, // 公网下载（可达数十分钟）
	"app_install":     true, // compose 拉镜像 + 起栈
	"ansible_run":     true, // playbook 分钟级
	"stack_upgrade":   true, // compose pull + up（大栈拉镜像可达数十分钟）
}

// taskTimeouts 任务级执行超时（防 executor 挂死占死 worker，见 run）。
// 未登记类型走 defaultTaskTimeout。
var taskTimeouts = map[string]time.Duration{
	"image_download":  60 * time.Minute, // 公网大镜像 + 慢源站
	"clone_vm":        30 * time.Minute, // 全盘拷贝大磁盘
	"ansible_run":     20 * time.Minute,
	"create_vm":       15 * time.Minute,
	"clone_image_vm":  15 * time.Minute,
	"delete_vm":       15 * time.Minute, // 多卷逐个删
	"cleanup_volumes": 15 * time.Minute,
	"app_install":     15 * time.Minute, // compose pull 不可控
	"stack_upgrade":   30 * time.Minute, // compose pull 大栈（ELK/Zabbix）可达数十分钟
	"stop_vm":         5 * time.Minute,  // ACPI 关机 + 兜底 destroy 本应分钟内
}

// defaultTaskTimeout 未登记任务类型的缺省执行上限。
const defaultTaskTimeout = 10 * time.Minute

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

// ErrQueueBusy 队列满入队超时的哨兵错误，由 Submit 返回给 handler。
// 旧实现此时仍返回「提交成功」，handler 照常回 202——前端拿着注定 failed 的
// task_id 轮询才知失败（假受理）；现在 Submit 如实报错，handler 的既有 err
// 分支直接把中文文案透给用户。
var ErrQueueBusy = errors.New(msgQueueBusy)

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
}

// NewManager 创建任务管理器：virt.New() 惰性连接，起快慢两组 worker goroutine。
// worker 只读各自队列，随进程一起退出，不做优雅关闭。
func NewManager(db *gorm.DB) *Manager {
	m := &Manager{
		DB:        db,
		Virt:      virt.New(),
		queueFast: make(chan uint, queueBufferSize),
		queueSlow: make(chan uint, queueBufferSize),
		executors: map[string]Executor{},
	}
	for i := 0; i < fastWorkerCount; i++ {
		go m.loop(m.queueFast)
	}
	for i := 0; i < slowWorkerCount; i++ {
		go m.loop(m.queueSlow)
	}
	m.sweepOrphanRunning()
	return m
}

// sweepOrphanRunning 启动时把上一进程遗留的 running/pending 任务置为 failed：
// 任务队列只在内存中（channel），进程重启后这些行永远不会被执行，不扫就是僵尸任务。
func (m *Manager) sweepOrphanRunning() {
	res := m.DB.Model(&model.Task{}).
		Where("status IN ?", []string{statusRunning, statusPending}).
		Updates(map[string]interface{}{"status": statusFailed, "error": "服务重启，任务已中断"})
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

	m.mu.RLock()
	fn, ok := m.executors[task.Type]
	m.mu.RUnlock()
	if !ok {
		// 终态写入失败会导致任务永久卡 running（全量审计 P1），走重试 + 留痕
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
			m.updateTaskFields(id, map[string]interface{}{
				"status": statusFailed,
				"error":  "任务参数损坏，无法执行",
			}, "任务参数损坏")
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
		if err != nil {
			// 原始错误链（含 libvirt 具体报错）只进日志，DB 只存 friendly 中文（该字段回显前端）
			log.Printf("[tasks] 任务失败 id=%d type=%s vm=%s err=%v", id, task.Type, task.VMName, err)
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
		log.Printf("[tasks] !!! 任务执行超时 id=%d type=%s vm=%s timeout=%s", id, task.Type, task.VMName, timeout)
		m.markFailed(id, fmt.Sprintf("任务执行超时（上限 %s），已自动终止，请检查环境后重试", timeout))
		return
	}
	// 成功终态：写失败同样会造僵尸任务，必须走重试 + 留痕（旧实现 _ = 静默吞掉）
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
