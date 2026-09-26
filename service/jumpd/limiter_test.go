package jumpd

import (
	"testing"
	"time"
)

// fakeClock 可注入时钟：测试推进时间窗口不靠 sleep（handler 版 loginLimiter 直接 time.Now()
// 导致窗口推进只能靠真实等待，jumpd 版按计划改进这一点）
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time     { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter() (*limiter, *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)}
	return newLimiterWithClock(time.Minute, 5, c.Now), c
}

func TestLimiterLockAfterMaxFails(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 3; i++ {
		l.fail("10.0.0.1")
	}
	if locked, _ := l.blocked("10.0.0.1"); locked {
		t.Fatalf("第 3 次失败不应触发锁定")
	}
	for i := 0; i < 2; i++ {
		l.fail("10.0.0.1")
	}
	locked, remain := l.blocked("10.0.0.1")
	if !locked {
		t.Fatalf("第 5 次失败后应锁定")
	}
	if remain <= 0 {
		t.Fatalf("锁定状态下剩余等待应 > 0，得到 %v", remain)
	}
}

func TestLimiterWindowReset(t *testing.T) {
	l, clock := newTestLimiter()
	for i := 0; i < 5; i++ {
		l.fail("10.0.0.2")
	}
	if locked, _ := l.blocked("10.0.0.2"); !locked {
		t.Fatalf("5 次失败后应锁定")
	}
	// 锁定中的 IP 再 Fail 不延长锁定：resetAt 固定于首次失败，剩余等待不变
	_, remainBefore := l.blocked("10.0.0.2")
	clock.Advance(10 * time.Second)
	l.fail("10.0.0.2")
	_, remainAfter := l.blocked("10.0.0.2")
	if remainAfter >= remainBefore {
		t.Fatalf("锁定中的额外失败不应延长锁定：before=%v after=%v", remainBefore, remainAfter)
	}
	// 窗口整体滑过 → 解锁且重新计数
	clock.Advance(time.Minute)
	if locked, _ := l.blocked("10.0.0.2"); locked {
		t.Fatalf("窗口滑过后应解锁")
	}
	for i := 0; i < 4; i++ {
		l.fail("10.0.0.2")
	}
	if locked, _ := l.blocked("10.0.0.2"); locked {
		t.Fatalf("新窗口内 4 次失败不应锁定")
	}
	l.fail("10.0.0.2")
	if locked, _ := l.blocked("10.0.0.2"); !locked {
		t.Fatalf("新窗口内累计满 5 次应重新锁定")
	}
}

func TestLimiterSuccessClears(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 4; i++ {
		l.fail("10.0.0.3")
	}
	l.success("10.0.0.3")
	for i := 0; i < 4; i++ {
		l.fail("10.0.0.3")
	}
	if locked, _ := l.blocked("10.0.0.3"); locked {
		t.Fatalf("Success 清零后 4 次失败不应锁定")
	}
	l.fail("10.0.0.3")
	if locked, _ := l.blocked("10.0.0.3"); !locked {
		t.Fatalf("清零后再错满 5 次应锁定")
	}
}

func TestLimiterPerIP(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 5; i++ {
		l.fail("10.0.0.4")
	}
	if locked, _ := l.blocked("10.0.0.4"); !locked {
		t.Fatalf("A IP 应锁定")
	}
	if locked, _ := l.blocked("10.0.0.5"); locked {
		t.Fatalf("A IP 锁定不应影响 B IP")
	}
}
