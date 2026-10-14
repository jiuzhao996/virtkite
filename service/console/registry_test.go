package console

import (
	"errors"
	"testing"
)

// TestRegistryDisconnectNoLiveConn 覆盖 Registry 不依赖 DB 的纯逻辑：对无可控连接的
// 会话强制断开必须返回 ErrNoLiveConn 哨兵，供 handler 识别后翻译成友好提示。
//
// 风险点：handler/session.go 用 `err == console.ErrNoLiveConn`（直接比较，不是 errors.Is）
// 判定「该会话没有可断的实时连接」（VNC 走 websockify 中转，服务端无法切断其 TCP）。
// 若这个哨兵从变量改成每次 new 的新值、或包装了一层，`==` 就会失效，管理员点「断开」
// 会把正常结果当成失败弹错。此用例同时钉死「同一实例」与「无锁早退」两条路径。
func TestRegistryDisconnectNoLiveConn(t *testing.T) {
	r := NewRegistry(nil)

	// 未登记的会话：conns 中无条目，直接返回哨兵，绝不触碰 DB。
	err := r.Disconnect(999)
	if err == nil {
		t.Fatal("对未登记会话 Disconnect 应返回错误而非 nil")
	}
	// 必须是同一个哨兵实例（handler 用 == 判定）。
	if err != ErrNoLiveConn {
		t.Fatalf("应返回 ErrNoLiveConn 实例本身，实际 %v（%T）", err, err)
	}
	if !errors.Is(err, ErrNoLiveConn) {
		t.Fatal("ErrNoLiveConn 应可被 errors.Is 识别")
	}
	if ErrNoLiveConn.Error() == "" {
		t.Fatal("ErrNoLiveConn 文案不应为空")
	}
}

// TestNewRegistryInitializesMaps NewRegistry 必须初始化两张内部 map，
// 否则首次 Open/OpenVNC/Disconnect 会向 nil map 写入而 panic。
//
// 风险点：conns / byToken 是写入型 map（非只读），nil map 写入会 panic；而
// NewRegistry 若漏初始化，只有在真实会话建立时才暴露，属于「上线才炸」的坑。
func TestNewRegistryInitializesMaps(t *testing.T) {
	r := NewRegistry(nil)
	if r.conns == nil {
		t.Fatal("conns map 未初始化，首次 Open 会 panic")
	}
	if r.byToken == nil {
		t.Fatal("byToken map 未初始化，首次 OpenVNC 会 panic")
	}
	// 空注册表下 Close 不得 panic（nil DB 场景：DB 未注入时收敛退化为纯内存清理）。
	r.Close(123)
}
