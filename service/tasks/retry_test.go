package tasks

import (
	"testing"

	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// newRetryTestManager 构造只用于重试扫描单测的 Manager（不起 worker/DB 连接）。
func newRetryTestManager(db *gorm.DB) *Manager {
	return &Manager{
		DB:        db,
		queueFast: make(chan uint, 8),
		queueSlow: make(chan uint, 8),
		executors: map[string]Executor{},
		loggers:   map[uint]*TaskLogger{},
	}
}

// TestRetryWhitelistBoundary 重试白名单边界：只有幂等安全的类型可自动重试。
func TestRetryWhitelistBoundary(t *testing.T) {
	retryable := []string{"image_download", "image_build", "stop_vm"}
	for _, ty := range retryable {
		if !isRetryableType(ty) {
			t.Errorf("类型 %s 应可自动重试", ty)
		}
		if maxRetryFor(ty) <= 0 {
			t.Errorf("类型 %s 的重试上限应 > 0", ty)
		}
	}
	// 会造成二次副作用的类型必须排除（尤其 create_vm 是题目红线）
	notRetryable := []string{"create_vm", "delete_vm", "clone_vm", "clone_image_vm", "cleanup_volumes", "ansible_run", "app_install", "stack_upgrade"}
	for _, ty := range notRetryable {
		if isRetryableType(ty) {
			t.Errorf("类型 %s 不应自动重试（非幂等安全）", ty)
		}
		if maxRetryFor(ty) != 0 {
			t.Errorf("类型 %s 的重试上限应为 0", ty)
		}
	}
}

// TestScanRetriesRequeuesIdempotentTask 白名单内的失败任务被 CAS 认领后复位为 pending 并重投。
func TestScanRetriesRequeuesIdempotentTask(t *testing.T) {
	db := openTaskTestDB(t)
	m := newRetryTestManager(db)

	task := &model.Task{Type: "image_download", Status: statusFailed, Error: "下载失败", MaxRetry: 2}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}

	m.scanRetriesOnce()

	var got model.Task
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatalf("回读任务失败: %v", err)
	}
	if got.Status != statusPending {
		t.Errorf("重投后状态应为 pending，实际 %s", got.Status)
	}
	if got.RetryCount != 1 {
		t.Errorf("retry_count 应为 1，实际 %d", got.RetryCount)
	}
	if got.RetryState != "" {
		t.Errorf("retry_state 应复位为空，实际 %q", got.RetryState)
	}
	if got.Error != "" {
		t.Errorf("重投应清空 error，实际 %q", got.Error)
	}
	select {
	case id := <-m.queueSlow:
		if id != task.ID {
			t.Errorf("重投的 taskID 错误：期望 %d，实际 %d", task.ID, id)
		}
	default:
		t.Fatal("失败任务未被重投进队列")
	}
}

// TestScanRetriesNoDoubleProcess 已被处理（状态非 failed）的任务不会被重复处理。
func TestScanRetriesNoDoubleProcess(t *testing.T) {
	db := openTaskTestDB(t)
	m := newRetryTestManager(db)

	task := &model.Task{Type: "image_build", Status: statusFailed, MaxRetry: 2}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	m.scanRetriesOnce() // 第一次：认领 + 复位为 pending + 重投
	m.scanRetriesOnce() // 第二次：任务已是 pending，不再匹配

	var got model.Task
	_ = db.First(&got, task.ID).Error
	if got.RetryCount != 1 {
		t.Errorf("不应被重复处理：retry_count 期望 1，实际 %d", got.RetryCount)
	}
	if len(m.queueSlow) != 1 {
		t.Errorf("不应重复重投：队列期望 1，实际 %d", len(m.queueSlow))
	}
}

// TestScanRetriesCASClaimGuard 处于 claiming 状态的行被候选查询排除（行级 CAS 防重）。
func TestScanRetriesCASClaimGuard(t *testing.T) {
	db := openTaskTestDB(t)
	m := newRetryTestManager(db)

	task := &model.Task{Type: "image_download", Status: statusFailed, MaxRetry: 2, RetryState: retryStateClaiming}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	m.scanRetriesOnce()

	var got model.Task
	_ = db.First(&got, task.ID).Error
	if got.Status != statusFailed || got.RetryCount != 0 {
		t.Errorf("claiming 行不应被再次处理：status=%s retry_count=%d", got.Status, got.RetryCount)
	}
}

// TestScanRetriesStopsAtMaxRetry 达到重试上限后不再重投。
func TestScanRetriesStopsAtMaxRetry(t *testing.T) {
	db := openTaskTestDB(t)
	m := newRetryTestManager(db)

	task := &model.Task{Type: "image_download", Status: statusFailed, MaxRetry: 2, RetryCount: 2}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	m.scanRetriesOnce()

	var got model.Task
	_ = db.First(&got, task.ID).Error
	if got.Status != statusFailed {
		t.Errorf("超限任务不应被重投：status=%s", got.Status)
	}
	if len(m.queueSlow) != 0 {
		t.Errorf("超限任务不应入队，实际队列 %d", len(m.queueSlow))
	}
}

// TestScanRetriesSkipsNonWhitelisted 即便 max_retry 被历史脏数据写成 >0，非白名单类型也不重投。
func TestScanRetriesSkipsNonWhitelisted(t *testing.T) {
	db := openTaskTestDB(t)
	m := newRetryTestManager(db)

	task := &model.Task{Type: "create_vm", Status: statusFailed, MaxRetry: 2}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	m.scanRetriesOnce()

	var got model.Task
	_ = db.First(&got, task.ID).Error
	if got.Status != statusFailed || got.RetryCount != 0 {
		t.Errorf("非白名单类型不应重投：status=%s retry_count=%d", got.Status, got.RetryCount)
	}
}

// TestScanRetriesSkipsAborted 进程重启清扫标记为 aborted 的任务不参与自动重试。
func TestScanRetriesSkipsAborted(t *testing.T) {
	db := openTaskTestDB(t)
	m := newRetryTestManager(db)

	task := &model.Task{Type: "image_download", Status: statusFailed, MaxRetry: 2, RetryState: retryStateAborted}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	m.scanRetriesOnce()

	var got model.Task
	_ = db.First(&got, task.ID).Error
	if got.RetryCount != 0 {
		t.Errorf("aborted 任务不应被重投：retry_count=%d", got.RetryCount)
	}
}

// TestSweepOrphanRunningMarksAborted 进程重启清扫把遗留任务标记为 aborted（不参与自动重试）。
func TestSweepOrphanRunningMarksAborted(t *testing.T) {
	db := openTaskTestDB(t)
	m := newRetryTestManager(db)

	task := &model.Task{Type: "image_download", Status: statusRunning, MaxRetry: 2}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	m.sweepOrphanRunning()

	var got model.Task
	_ = db.First(&got, task.ID).Error
	if got.Status != statusFailed {
		t.Errorf("遗留 running 任务应被置 failed，实际 %s", got.Status)
	}
	if got.RetryState != retryStateAborted {
		t.Errorf("遗留任务应标记 aborted，实际 %q", got.RetryState)
	}
	// 清扫后即便再扫描也不该重投
	m.scanRetriesOnce()
	if got.RetryCount != 0 {
		t.Errorf("aborted 任务不应被重投：retry_count=%d", got.RetryCount)
	}
}
