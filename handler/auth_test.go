package handler

import (
	"sync"
	"testing"
	"time"
)

// TestLoginLimiter 登录失败限流：计数、锁定、成功清零、窗口过期。
func TestLoginLimiter(t *testing.T) {
	t.Run("失败累计达上限即锁定", func(t *testing.T) {
		l := newLoginLimiter(time.Minute, 5)
		for i := 0; i < 5; i++ {
			if blocked, _ := l.blocked("1.2.3.4"); blocked {
				t.Fatalf("第 %d 次失败不应锁定", i+1)
			}
			l.fail("1.2.3.4")
		}
		if blocked, wait := l.blocked("1.2.3.4"); !blocked || wait <= 0 {
			t.Errorf("5 次失败后应锁定且有剩余等待时长, blocked=%v wait=%v", blocked, wait)
		}
		// 不同 IP 互不影响
		if blocked, _ := l.blocked("5.6.7.8"); blocked {
			t.Error("其他 IP 不应被连带锁定")
		}
	})

	t.Run("成功登录清零计数", func(t *testing.T) {
		l := newLoginLimiter(time.Minute, 5)
		for i := 0; i < 4; i++ {
			l.fail("1.2.3.4")
		}
		l.success("1.2.3.4")
		for i := 0; i < 4; i++ { // 清零后可再承受 4 次失败（第 5 次锁定）
			l.fail("1.2.3.4")
		}
		if blocked, _ := l.blocked("1.2.3.4"); blocked {
			t.Error("成功清零后不应立即锁定")
		}
		l.fail("1.2.3.4")
		if blocked, _ := l.blocked("1.2.3.4"); !blocked {
			t.Error("清零后重新累计达上限应锁定")
		}
	})

	t.Run("窗口过期解锁（短窗口实测）", func(t *testing.T) {
		l := newLoginLimiter(30*time.Millisecond, 1)
		l.fail("1.2.3.4")
		if blocked, _ := l.blocked("1.2.3.4"); !blocked {
			t.Fatal("窗口内应处于锁定")
		}
		time.Sleep(40 * time.Millisecond)
		if blocked, _ := l.blocked("1.2.3.4"); blocked {
			t.Error("窗口过期后应解锁")
		}
	})

	t.Run("并发安全", func(t *testing.T) {
		l := newLoginLimiter(time.Minute, 1000)
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				l.fail("concurrent")
				l.blocked("concurrent")
				l.success("concurrent")
			}()
		}
		wg.Wait()
	})
}

// TestRound1 历史曲线数值保留一位小数。
func TestRound1(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{13.26, 13.3},
		{13.24, 13.2},
		{0, 0},
	}
	for _, c := range cases {
		if got := round1(c.in); got != c.want {
			t.Errorf("round1(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
