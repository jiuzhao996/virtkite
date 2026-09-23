package dbx

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// fakeDB 返回一个非空的 *gorm.DB 占位：本包只对它做 nil 判断，不会真正发起 SQL，
// 写入结果完全由注入的 fn 决定，因此单测不连真数据库、不起库。
func fakeDB() *gorm.DB { return &gorm.DB{} }

// errBoom 模拟 DB 抖动（导出为包内变量，便于用 errors.Is 校验错误链）。
var errBoom = errors.New("boom: 模拟 DB 抖动")

// fakeWrite 返回受控失败的写入函数与调用计数指针：前 failTimes 次返回 errBoom，
// 之后返回 nil；failTimes 传 -1 表示永远失败。
func fakeWrite(failTimes int) (func() error, *int) {
	calls := 0
	return func() error {
		calls++
		if failTimes < 0 || calls <= failTimes {
			return errBoom
		}
		return nil
	}, &calls
}

// captureLog 捕获测试期间的 log 输出（标准库 log 的全局输出，包内测试不并行，安全）。
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	out, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(out)
		log.SetFlags(flags)
	}()
	fn()
	return buf.String()
}

func TestPersistFirstTrySuccess(t *testing.T) {
	fn, calls := fakeWrite(0)
	var err error
	logs := captureLog(t, func() { err = Persist(fakeDB(), "首次成功", fn) })
	if err != nil {
		t.Fatalf("首次成功不应返回错误: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("首次成功只应执行 1 次，实际 %d", *calls)
	}
	if strings.Contains(logs, "写入失败") {
		t.Fatalf("首次成功不应留失败日志: %q", logs)
	}
}

func TestPersistRetryThenSuccess(t *testing.T) {
	fn, calls := fakeWrite(2) // 前两次失败，第三次成功
	var err error
	start := time.Now()
	logs := captureLog(t, func() { err = Persist(fakeDB(), "重试后成功", fn) })
	if err != nil {
		t.Fatalf("重试后成功不应返回错误: %v", err)
	}
	if *calls != maxAttempts {
		t.Fatalf("应重试到第 %d 次才成功，实际调用 %d 次", maxAttempts, *calls)
	}
	for _, want := range []string{
		"[persist] 写入失败 场景=重试后成功 第 1/3 次",
		"[persist] 写入失败 场景=重试后成功 第 2/3 次",
		"[persist] 写入重试成功 场景=重试后成功 第 3 次",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("缺少日志 %q，实际: %q", want, logs)
		}
	}
	if strings.Contains(logs, "[persist] !!!") {
		t.Fatalf("重试成功不应打醒目失败日志: %q", logs)
	}
	// 退避 100ms + 200ms，三次最多约 300ms，远低于 1 秒上限
	if el := time.Since(start); el > time.Second {
		t.Fatalf("重试总耗时 %v 超过 1 秒上限", el)
	}
}

func TestPersistAllFailedReturnsErrorAndLogs(t *testing.T) {
	fn, calls := fakeWrite(-1) // 永远失败
	var err error
	start := time.Now()
	logs := captureLog(t, func() { err = Persist(fakeDB(), "三次全败", fn) })
	if err == nil {
		t.Fatal("三次全败必须返回错误")
	}
	if !errors.Is(err, errBoom) {
		t.Fatalf("返回错误必须用 %%w 保留原始错误链: %v", err)
	}
	if !strings.Contains(err.Error(), "三次全败") {
		t.Fatalf("返回错误应带场景名便于定位: %v", err)
	}
	if *calls != maxAttempts {
		t.Fatalf("应恰好尝试 %d 次，实际 %d", maxAttempts, *calls)
	}
	if !strings.Contains(logs, "[persist] !!! 写入最终失败（3 次均失败，状态可能已漂移）场景=三次全败") {
		t.Fatalf("全败必须打醒目日志: %q", logs)
	}
	if n := strings.Count(logs, "[persist] 写入失败"); n != maxAttempts {
		t.Fatalf("每次失败都应留痕，期望 %d 条，实际 %d", maxAttempts, n)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("三次全败总耗时 %v 超过 1 秒上限", el)
	}
}

func TestPersistNilDBDoesNotCallFn(t *testing.T) {
	called := false
	var err error
	logs := captureLog(t, func() {
		err = Persist(nil, "DB 未注入", func() error { called = true; return nil })
	})
	if err == nil {
		t.Fatal("DB 为空应返回错误")
	}
	if called {
		t.Fatal("DB 为空时不应执行 fn（否则调用方漏注入会 nil panic）")
	}
	if !strings.Contains(logs, "[persist] !!!") {
		t.Fatalf("DB 为空应留醒目日志: %q", logs)
	}
}

func TestPersistBestEffortLogsWithoutReturningError(t *testing.T) {
	fn, calls := fakeWrite(-1)
	logs := captureLog(t, func() { PersistBestEffort(fakeDB(), "best-effort 全败", fn) })
	if *calls != maxAttempts {
		t.Fatalf("best-effort 也应重试到 %d 次，实际 %d", maxAttempts, *calls)
	}
	if !strings.Contains(logs, "[persist] !!! 写入最终失败") {
		t.Fatalf("best-effort 全败必须留醒目日志: %q", logs)
	}
}

func TestPersistBestEffortFirstTrySuccessQuiet(t *testing.T) {
	fn, calls := fakeWrite(0)
	logs := captureLog(t, func() { PersistBestEffort(fakeDB(), "best-effort 成功", fn) })
	if *calls != 1 {
		t.Fatalf("首次成功只应执行 1 次，实际 %d", *calls)
	}
	if strings.TrimSpace(logs) != "" {
		t.Fatalf("成功不应打任何日志: %q", logs)
	}
}

// captureLog 会替换全局 log writer，确认它已还原，避免污染其他测试的输出。
func TestCaptureLogRestoresWriter(t *testing.T) {
	before := log.Writer()
	captureLog(t, func() {})
	if log.Writer() != before {
		t.Fatal("captureLog 未还原 log 输出")
	}
}
