package jumpd

import (
	"sync"
	"time"
)

// limiter SSH 跳板登录失败限流器（内存实现，按来源 IP 计数）。
// 策略与 handler/auth.go 的 loginLimiter 同构：同一 IP 在统计窗口内失败达到上限后
// 锁定到窗口结束，成功登录即清零；目的同样是抬高口令爆破成本（纯内存，重启即重置）。
// 与 handler 版的差异：时间判定抽成可注入的 now 函数，测试可精确推进窗口不靠 sleep。
type limiter struct {
	mu       sync.Mutex
	fails    map[string]*failRecord
	window   time.Duration
	maxFails int
	now      func() time.Time
}

type failRecord struct {
	count   int
	resetAt time.Time
}

func newLimiter(window time.Duration, maxFails int) *limiter {
	return newLimiterWithClock(window, maxFails, time.Now)
}

func newLimiterWithClock(window time.Duration, maxFails int, now func() time.Time) *limiter {
	return &limiter{
		fails:    map[string]*failRecord{},
		window:   window,
		maxFails: maxFails,
		now:      now,
	}
}

// blocked 判断该 IP 是否处于锁定状态；锁定时返回剩余等待时长。
func (l *limiter) blocked(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.fails[ip]
	if !ok {
		return false, 0
	}
	if l.now().After(rec.resetAt) {
		delete(l.fails, ip) // 窗口已过，惰性清理
		return false, 0
	}
	if rec.count >= l.maxFails {
		return true, rec.resetAt.Sub(l.now())
	}
	return false, 0
}

// fail 记录一次失败；窗口内首次失败起算，达到上限即锁定。
// 锁定中的额外失败只累计不延长 resetAt（锁定时长固定，不被爆破者用持续失败续期）。
func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.fails) > 1024 { // 简单防膨胀：规模超阈值时先清一轮过期项
		for k, rec := range l.fails {
			if now.After(rec.resetAt) {
				delete(l.fails, k)
			}
		}
	}
	rec, ok := l.fails[ip]
	if !ok || now.After(rec.resetAt) {
		l.fails[ip] = &failRecord{count: 1, resetAt: now.Add(l.window)}
		return
	}
	rec.count++
}

// success 认证成功清零该 IP 计数。
func (l *limiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
