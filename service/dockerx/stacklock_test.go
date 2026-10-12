package dockerx

import (
	"sync"
	"testing"
	"time"
)

// TestTryStackLockExclusive 同一栈互斥、异栈独立、释放可重复。
func TestTryStackLockExclusive(t *testing.T) {
	release, ok := tryStackLock("stack-a")
	if !ok {
		t.Fatal("首次获取应成功")
	}
	if _, ok := tryStackLock("stack-a"); ok {
		t.Fatal("同栈第二次获取必须失败（互斥生效）")
	}
	// 异栈不受影响
	r2, ok := tryStackLock("stack-b")
	if !ok {
		t.Fatal("不同栈不应互相阻塞")
	}
	r2()

	release()
	release() // 重复释放不得 panic
	if _, ok := tryStackLock("stack-a"); !ok {
		t.Fatal("释放后应可再次获取")
	}
}

// TestTryStackLockConcurrent 并发抢占同一栈：任意时刻只能一人持有（无死锁、有胜者）。
func TestTryStackLockConcurrent(t *testing.T) {
	const n = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	var held, maxHeld int
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if release, ok := tryStackLock("stack-c"); ok {
				mu.Lock()
				held++
				if held > maxHeld {
					maxHeld = held
				}
				mu.Unlock()
				time.Sleep(time.Millisecond)
				mu.Lock()
				held--
				mu.Unlock()
				release()
			}
		}()
	}
	close(start)
	wg.Wait()
	if maxHeld > 1 {
		t.Fatalf("同一栈并发持有数超过 1：%d（互斥失效）", maxHeld)
	}
}
