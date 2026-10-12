// 栈级操作锁：把「同一 compose 栈的操作」串行化，异栈并行（借 Dockge 的终端名唯一性思路，
// 本平台无终端概念，改用进程内按栈名的互斥）。
//
// 与 vmlock 同款语义：非阻塞 TryLock，抢不到立即返回明确错误，绝不让调用方排队等待——
// compose up 可达分钟级，若阻塞等待，第二个请求会长时间挂住 HTTP 连接，体验更差。
//
// 为什么下沉到 dockerx（而非 handler 层）：所有栈操作的入口都汇聚到本包的
// ComposeAction / ComposeUpFromFile / ComposePull / ComposeServiceAction，
// 把锁放在这里，docker 编排、栈商店、设计器三条调用链自动共享同一把锁，
// 不会因为「某个新入口忘了加锁」而漏保护（同 vmssh 白名单下沉进构造函数的教训）。
package dockerx

import (
	"errors"
	"sync"
)

// ErrStackBusy 同一栈已有操作在进行中（handler 侧以中文文案回给用户）。
var ErrStackBusy = errors.New("该栈正在执行其他操作，请稍后再试")

// stackLocks 每个栈名一把锁。条目不主动删除：栈数量级个位数，常驻几把空锁的代价
// 远小于引入引用计数与删除竞态的复杂度（与 vmlock 同一取舍）。
var stackLocks sync.Map // map[string]*sync.Mutex

// IsStackBusy 探测某栈当前是否有操作在进行（非阻塞试锁后立即释放）。
//
// 供 handler 在提交异步栈任务（如回滚）前给用户即时 409 反馈，语义与 handler 的
// composeWriteErr 一致；真正把操作串行化的是任务内 ComposePull / ComposeUpFromFile
// 各自的持锁调用，本函数只是「先看一眼忙不忙」的尽力而为判定（检查与真正执行之间存在
// 微小时间窗，属可接受——窗口内若被别的操作抢锁，任务会以 ErrStackBusy 明确失败）。
func (d *Dockerx) IsStackBusy(name string) bool {
	if !safeDockerName(name) {
		return false
	}
	release, ok := tryStackLock(name)
	if ok {
		release()
		return false
	}
	return true
}

// tryStackLock 非阻塞获取栈名锁。成功返回 release（必须调用，建议 defer）与 true；
// 已被占用返回 (nil, false)。
func tryStackLock(name string) (release func(), ok bool) {
	mu, _ := stackLocks.LoadOrStore(name, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	if !m.TryLock() {
		return nil, false
	}
	var once sync.Once
	return func() { once.Do(m.Unlock) }, true
}
