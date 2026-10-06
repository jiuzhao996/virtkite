package jumpd

import "sync"

// liveSessions 进程内活跃跳板桥接登记：console_sessions.id → 强断钩子。
// jump 会话不进 console.Registry（那是 WS 连接表，见 service/console），
// Web 端「强制断开」的能力由本表提供——bridgeSession 起桥时登记、收桥时注销。
var liveSessions sync.Map // uint → func()

// registerLive 登记活跃桥接，返回注销函数（bridgeSession defer 调用）。
// sid 为 0（会话行登记失败，无 ID 可寻）或钩子为 nil 时不登记，返回空操作。
func registerLive(sid uint, kill func()) (unregister func()) {
	if sid == 0 || kill == nil {
		return func() {}
	}
	liveSessions.Store(sid, kill)
	return func() { liveSessions.Delete(sid) }
}

// DisconnectSession 强制断开进程内的跳板会话，返回是否命中在线桥接。
// 命中后由 kill 钩子收桥（关目标 SSH 客户端 + 用户 channel），会话行随后照常走
// closeJumpSession 收口；未命中 = 本进程内无此桥接（服务重启后的残留行等）。
// 钩子只调用一次（LoadAndDelete 原子摘除），注销侧的 Delete 幂等。
func DisconnectSession(id uint) bool {
	v, ok := liveSessions.LoadAndDelete(id)
	if !ok {
		return false
	}
	if kill, ok := v.(func()); ok && kill != nil {
		kill()
	}
	return true
}
