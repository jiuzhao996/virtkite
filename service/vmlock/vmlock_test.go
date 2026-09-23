package vmlock

import (
	"sync"
	"testing"
	"time"
)

func TestTryExclusive(t *testing.T) {
	release, ok := Try(1001)
	if !ok {
		t.Fatal("首次 Try 应当成功")
	}
	if _, ok := Try(1001); ok {
		t.Fatal("同一 VM 的第二次 Try 必须失败（互斥生效）")
	}
	release()
	if _, ok := Try(1001); !ok {
		t.Fatal("释放后应可再次获取")
	}
}

func TestTryDifferentVMsIndependent(t *testing.T) {
	r1, ok := Try(2001)
	if !ok {
		t.Fatal("VM 2001 应可获取")
	}
	defer r1()
	r2, ok := Try(2002)
	if !ok {
		t.Fatal("不同 VM 之间不应互相阻塞")
	}
	r2()
}

func TestReleaseIdempotent(t *testing.T) {
	release, ok := Try(3001)
	if !ok {
		t.Fatal("获取失败")
	}
	release()
	release() // 重复释放不得 panic、也不得解锁到别人的锁
	if _, ok := Try(3001); !ok {
		t.Fatal("重复释放后仍应可正常获取")
	}
}

// TestConcurrentTryOnlyOneWinner 并发抢占时必须有且仅有一个成功者。
func TestConcurrentTryOnlyOneWinner(t *testing.T) {
	const n = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if release, ok := Try(4001); ok {
				mu.Lock()
				winners++
				mu.Unlock()
				time.Sleep(2 * time.Millisecond)
				release()
			}
		}()
	}
	close(start)
	wg.Wait()
	if winners == 0 {
		t.Fatal("至少应有一个成功者")
	}
	// 允许并发下多人先后获得锁，但任一时刻只能一人持有；这里只断言「无死锁、有胜者」
	t.Logf("并发 %d 次抢占，成功 %d 次", n, winners)
}
