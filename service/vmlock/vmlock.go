// Package vmlock 提供「同一台虚拟机的操作互斥」，防止并发生命周期操作互相踩踏。
//
// 背景：虚拟机操作不是原子的——「读状态 → 调 libvirt → 回写 DB」三步之间没有任何隔离。
// 两个请求并发点「重启」和「删除」，就可能对正在 undefine 的域发 reboot，产生不可预期
// 的域状态与脏卷；双开、误触、脚本重放都会触发。
//
// 与既有防护的分工：
//   - vmlock：进程内互斥，拦住「同一瞬间并发进来的两个操作」（最刺手的一类）；
//   - submitTaskGuard / guardVMIdle：查库判断该 VM 是否已有在跑的异步任务，
//     覆盖「HTTP 已返回但后台任务还在跑」的时间窗。
//
// 两者互补，不互相替代：锁只在请求处理期间持有，后台任务的窗口由 guardVMIdle 兜。
package vmlock

import "sync"

// locks 每台虚拟机一把锁。条目不主动删除：以虚拟机数量为界，常驻几条空锁的代价
// 远小于引入引用计数与删除竞态的复杂度。
var locks sync.Map // map[uint]*sync.Mutex

// Try 非阻塞尝试获取 vmID 的操作锁。
// 成功返回 release 函数（必须调用，建议 defer）与 true；已被占用返回 (nil, false)。
// release 可安全重复调用。
func Try(vmID uint) (release func(), ok bool) {
	mu, _ := locks.LoadOrStore(vmID, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	if !m.TryLock() {
		return nil, false
	}
	var once sync.Once
	return func() { once.Do(m.Unlock) }, true
}
