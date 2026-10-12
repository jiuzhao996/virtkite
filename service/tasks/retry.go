// retry.go：任务自动重试扫描器（对应 XXL-JOB 的「失败重试」调度语义）。
//
// 设计要点：
//   - 独立扫描协程（每 retryScanInterval 一次），与执行路径解耦——重试决策不阻塞 worker；
//   - 行级 CAS（UPDATE ... WHERE id=? AND retry_state=?）靠 RowsAffected 判定是否抢到该行，
//     多实例/多次扫描并发时同一任务只会被重投一次；
//   - 只有登记在白名单里的「幂等安全」类型才自动重试（见 retryableTypes 取舍注释）；
//   - 自起协程自带 recover，panic 值与堆栈只进日志（项目规范 10）。
package tasks

import (
	"log"
	"runtime/debug"
	"time"

	"github.com/jiuzhao/vmops/model"
)

const (
	// retryScanInterval 重试扫描周期。10s 量级：够快吸收瞬时故障（RPC 抖动、源站 5xx），
	// 又不至于让失败任务立刻重投而掩盖「真故障」（给运维 10s 观察窗）。
	retryScanInterval = 10 * time.Second
	// retryScanBatch 单轮最多处理的任务数，防一次故障风暴把重投挤满队列。
	retryScanBatch = 20
)

// retryStateClaiming 扫描器认领某行时的 CAS 目标值（非空即被候选查询排除）。
const retryStateClaiming = "claiming"

// retryStateAborted 进程重启清扫后的状态：不再参与自动重试（被中断的任务不自动重投）。
const retryStateAborted = "aborted"

// retryableTypes 自动重试白名单：类型 → 允许的最大重试次数。
//
// 取舍标准只有一条——「把同一任务原样再跑一遍，是否幂等安全」：
//   - image_download：目标文件已存在即跳过下载、直接登记镜像（execImageDownload 显式幂等），
//     重跑最多白耗一次流量，不会产生重复镜像行（按 path 复用登记）。
//   - image_build：docker build 对同一 tag 重跑幂等（命中层缓存），产物一致。
//   - stop_vm：关机是幂等操作（已关机再关为 no-op），失败多为瞬时 libvirt RPC 抖动。
//
// 明确排除（会重复产生副作用，绝不自动重试）：
//   - create_vm / clone_vm / clone_image_vm：会重复建卷/建机（题目红线）；
//   - delete_vm / cleanup_volumes：部分删除后重跑状态不明，可能误删或残留；
//   - app_install / ansible_run / stack_upgrade：脚本与 playbook 未必幂等，重跑可能重复安装。
var retryableTypes = map[string]int{
	"image_download": 2,
	"image_build":    2,
	"stop_vm":        2,
}

// isRetryableType 该任务类型是否允许自动重试。
func isRetryableType(taskType string) bool {
	_, ok := retryableTypes[taskType]
	return ok
}

// maxRetryFor 返回该类型的自动重试上限（未登记为 0 = 不重试）。
func maxRetryFor(taskType string) int {
	return retryableTypes[taskType]
}

// StartRetryScanner 启动重试扫描协程。随进程退出，不提供关闭入口（与 worker 池同生命周期）。
func (m *Manager) StartRetryScanner() {
	go func() {
		defer func() {
			// 兜底：即便 scanRetriesOnce 内部 recover 也漏网，也不让协程把进程带走
			if r := recover(); r != nil {
				log.Printf("[tasks] 重试扫描协程 panic panic=%v\n%s", r, debug.Stack())
			}
		}()
		ticker := time.NewTicker(retryScanInterval)
		defer ticker.Stop()
		for range ticker.C {
			m.scanRetriesOnce()
		}
	}()
}

// scanRetriesOnce 单轮扫描：找可重试的失败任务，CAS 认领后重投。
// 内部自带 recover——一轮扫描里的意外（如脏数据）不该让整个扫描协程退出。
func (m *Manager) scanRetriesOnce() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tasks] 重试扫描单轮 panic panic=%v\n%s", r, debug.Stack())
		}
	}()

	var cands []model.Task
	// 候选：失败 + 未超重试上限 + 空闲（retry_state=''）。retry_state 非空的行（认领中/已中止）跳过。
	if err := m.DB.
		Where("status = ? AND retry_state = ? AND max_retry > retry_count", statusFailed, "").
		Order("id ASC").Limit(retryScanBatch).Find(&cands).Error; err != nil {
		log.Printf("[tasks] 重试扫描查询失败 err=%v", err)
		return
	}
	for _, t := range cands {
		// 白名单复核：max_retry 是提交时写入的快照，仍按当前白名单二次确认（防历史脏数据）
		if !isRetryableType(t.Type) {
			continue
		}
		// 行级 CAS：只有把 retry_state 从 '' 抢成 'claiming' 的那次更新才算认领成功
		res := m.DB.Model(&model.Task{}).
			Where("id = ? AND retry_state = ?", t.ID, "").
			Update("retry_state", retryStateClaiming)
		if res.Error != nil {
			log.Printf("[tasks] 重试认领失败 id=%d err=%v", t.ID, res.Error)
			continue
		}
		if res.RowsAffected != 1 {
			continue // 未抢到（已被处理），跳过
		}
		m.requeueForRetry(t)
	}
}

// requeueForRetry 把认领到的失败任务复位为 pending 并重新入队。
func (m *Manager) requeueForRetry(t model.Task) {
	newCount := t.RetryCount + 1
	// 复位为 pending 并自增 retry_count；retry_state 归 ''（若本次仍失败且未超限，可再被扫描）
	res := m.DB.Model(&model.Task{}).Where("id = ?", t.ID).Updates(map[string]interface{}{
		"status":      statusPending,
		"retry_count": newCount,
		"retry_state": "",
		"error":       "",
	})
	if res.Error != nil {
		log.Printf("[tasks] 重试复位失败 id=%d err=%v", t.ID, res.Error)
		// 复位失败：把 retry_state 退回 '' 让下一轮扫描重来（否则该行永久卡在 claiming）
		m.DB.Model(&model.Task{}).Where("id = ?", t.ID).Update("retry_state", "")
		return
	}
	log.Printf("[tasks] 自动重试 id=%d type=%s vm=%s 第 %d/%d 次",
		t.ID, t.Type, t.VMName, newCount, t.MaxRetry)

	// 重新入队（按类型选池）。队列满则如实置 failed——下一轮扫描若仍未超限会再试。
	task := &model.Task{ID: t.ID, Type: t.Type, VMName: t.VMName}
	if err := m.enqueue(task); err != nil {
		m.markFailed(t.ID, msgQueueBusy)
	}
}
