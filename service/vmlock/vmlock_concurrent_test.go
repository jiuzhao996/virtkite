package vmlock

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestTryMutualExclusionUnderContention 严格验证并发抢占下的互斥性质。
//
// 风险点：vmlock 的存在意义就是「同一瞬间并发进来的两个生命周期操作不互相踩踏」
// （双开、误触、脚本重放）。已有的 TestConcurrentTryOnlyOneWinner 只断言「有胜者、
// 无死锁」，刻意没有验证「任一时刻至多一人持有」——那恰恰是锁的核心契约。若日后有人
// 把 Try 的 TryLock 误改成 Lock（或去掉锁），那个用例仍会通过，而互斥已被破坏。
// 这里用原子计数记录「同时持有者数量」的峰值，必须恒为 1。
func TestTryMutualExclusionUnderContention(t *testing.T) {
	const goroutines = 100
	var (
		active    int32 // 当前持有锁的 goroutine 数
		maxActive int32 // 观测到的持有者峰值
		acquired  int32 // 成功获取次数
	)
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, ok := Try(4242)
			if !ok {
				return
			}
			atomic.AddInt32(&acquired, 1)
			n := atomic.AddInt32(&active, 1)
			// 无锁更新峰值（CAS 循环）
			for {
				m := atomic.LoadInt32(&maxActive)
				if n <= m || atomic.CompareAndSwapInt32(&maxActive, m, n) {
					break
				}
			}
			// 持锁一小段时间，给其他 goroutine 制造抢占窗口
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&active, -1)
			release()
		}()
	}

	close(start)
	wg.Wait()

	if atomic.LoadInt32(&maxActive) != 1 {
		t.Fatalf("互斥被破坏：同时持有者峰值 = %d，期望恒为 1", atomic.LoadInt32(&maxActive))
	}
	if atomic.LoadInt32(&acquired) == 0 {
		t.Fatal("并发抢占不应无一人成功")
	}
}

// TestTryOnZeroVMID 边界：vmID=0 也是合法的 map 键，锁语义应与其它 id 一致
// （确认实现没有对 0 做特殊处理或退化）。
func TestTryOnZeroVMID(t *testing.T) {
	r1, ok := Try(0)
	if !ok {
		t.Fatal("vmID=0 首次 Try 应成功")
	}
	if _, ok := Try(0); ok {
		t.Fatal("vmID=0 的第二次 Try 必须失败（与其它 id 语义一致）")
	}
	r1()
	if r2, ok := Try(0); !ok {
		t.Fatal("释放后 vmID=0 应可再次获取")
	} else {
		r2()
	}
}
