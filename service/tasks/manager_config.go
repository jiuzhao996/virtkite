// 本文件集中定义任务管理器的常量、分池/超时配置表与哨兵错误，便于毕设讲解
// 「哪些任务走快池、哪些走慢池、各类型超时多久、任务状态与文案取值」。
package tasks

import (
	"errors"
	"time"
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
	"image_finalize":  true, // virt-sysprep + customize + sparsify（分钟级 IO）
	"image_build":     true, // docker build（拉基础镜像 + 逐层构建，分钟级）
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
	"image_finalize":  45 * time.Minute, // sysprep + customize + sparsify（全盘拷贝慢）
	"image_build":     30 * time.Minute, // docker build（含拉基础镜像）
	"stop_vm":         5 * time.Minute,  // ACPI 关机 + 兜底 destroy 本应分钟内
}

// defaultTaskTimeout 未登记任务类型的缺省执行上限。
const defaultTaskTimeout = 10 * time.Minute

const (
	// statusPending 任务等待执行。
	statusPending = "pending"
	// statusRunning 任务执行中。
	statusRunning = "running"
	// statusCancelled 任务被用户取消（AU2）：仅对响应 ctx 的 executor 立即生效
	//（ansible 子进程被 kill）；不响应 ctx 的（libvirt RPC）底层操作可能仍在
	// 后台跑完，但任务状态以取消为准，不再回写其它终态。
	statusCancelled = "cancelled"
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
