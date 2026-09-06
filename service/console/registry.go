package console

import (
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// StaleAfterResolver 返回 VNC 会话无活动判定过期的时长（超过该时长无 token 解析即视为掉线，
// 由清扫器收敛）。main 启动时接到系统设置（service/setting），未接线时退回默认 60 分钟。
var StaleAfterResolver = func() time.Duration { return 60 * time.Minute }

// Registry 控制台会话注册表（单进程内存 + DB 持久）。
// SSH/串口 WS 连接持有在此，可服务端强制断开；VNC token 映射用于解析事件刷新存活。
// 持有的连接一律是写入串行化的 *Conn（见 conn.go），因为强制断开与桥接转发会并发写同一连接。
type Registry struct {
	mu      sync.Mutex
	DB      *gorm.DB
	conns   map[uint]*Conn  // sessionID → WS 连接（仅 ssh/serial）
	byToken map[string]uint // vnc token → sessionID
}

// NewRegistry 创建会话注册表。
func NewRegistry(db *gorm.DB) *Registry {
	return &Registry{
		DB:      db,
		conns:   make(map[uint]*Conn),
		byToken: make(map[string]uint),
	}
}

// StartSweeper 启动过期清扫（VNC 无关闭事件，超 StaleAfterResolver 时长未解析即标记 closed）。
// 清扫器为常驻后台 goroutine，单轮 panic 由 sweepOnce 自行兜底，不会终止定时循环。
func (r *Registry) StartSweeper() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			r.sweepOnce()
		}
	}()
}

// sweepOnce 执行一轮清扫：收敛过期 VNC 会话 + 清理已关闭会话的 token 映射。
// 后台 goroutine 内的 panic 无法被 gin Recovery 拦截，会直接终止进程，故此处必须自兜底。
func (r *Registry) sweepOnce() {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[console] 会话清扫 panic=%v\n%s", rec, debug.Stack())
		}
	}()

	cutoff := time.Now().Add(-StaleAfterResolver())
	if err := r.DB.Model(&model.ConsoleSession{}).
		Where("status = ? AND type = ? AND last_seen < ?", "active", "vnc", cutoff).
		Updates(map[string]interface{}{"status": "closed", "ended_at": time.Now()}).Error; err != nil {
		log.Printf("[console] 收敛过期 VNC 会话失败: %v", err)
	}

	// 先在锁内取快照，逐条查库在锁外进行：持锁做 DB IO 会阻塞所有会话的开启与关闭
	r.mu.Lock()
	snapshot := make(map[string]uint, len(r.byToken))
	for token, id := range r.byToken {
		snapshot[token] = id
	}
	r.mu.Unlock()

	stale := make([]string, 0, len(snapshot))
	for token, id := range snapshot {
		var s model.ConsoleSession
		if err := r.DB.Select("status").First(&s, id).Error; err != nil || s.Status != "active" {
			stale = append(stale, token)
		}
	}
	if len(stale) == 0 {
		return
	}

	r.mu.Lock()
	for _, token := range stale {
		// 二次确认映射未在锁外查库期间被重新签发覆盖
		if id, ok := r.byToken[token]; ok && snapshot[token] == id {
			delete(r.byToken, token)
		}
	}
	r.mu.Unlock()
}

// Open 创建 WS 会话（ssh/serial）并持有连接，返回 DB 记录。
// conn 必须是写入串行化的 *Conn：强制断开会与桥接转发并发写同一连接。
func (r *Registry) Open(vmID uint, vmName, typ, username string, userID *uint, clientIP string, conn *Conn) *model.ConsoleSession {
	now := time.Now()
	s := &model.ConsoleSession{
		VMID: vmID, VMName: vmName, Type: typ,
		UserID: userID, Username: username, ClientIP: clientIP,
		Status: "active", StartedAt: now, LastSeen: now,
	}
	if err := r.DB.Create(s).Error; err != nil {
		return nil
	}
	r.mu.Lock()
	r.conns[s.ID] = conn
	r.mu.Unlock()
	return s
}

// OpenVNC 创建 VNC 会话并建立 token 映射（关闭事件不可见，靠清扫器收敛）。
func (r *Registry) OpenVNC(vmID uint, vmName, username string, userID *uint, clientIP, token string) *model.ConsoleSession {
	now := time.Now()
	s := &model.ConsoleSession{
		VMID: vmID, VMName: vmName, Type: "vnc",
		UserID: userID, Username: username, ClientIP: clientIP,
		Status: "active", StartedAt: now, LastSeen: now, Token: token,
	}
	if err := r.DB.Create(s).Error; err != nil {
		return nil
	}
	r.mu.Lock()
	r.byToken[token] = s.ID
	r.mu.Unlock()
	return s
}

// TouchByToken 刷新 VNC token 存活（websockify 解析时调用）。
func (r *Registry) TouchByToken(token string) {
	r.mu.Lock()
	id, ok := r.byToken[token]
	r.mu.Unlock()
	if !ok {
		return
	}
	now := time.Now()
	_ = r.DB.Model(&model.ConsoleSession{ID: id}).Updates(map[string]interface{}{
		"last_seen": now,
	})
}

// Close 标记会话关闭并释放持有资源（WS 关闭/连接断开时调用，幂等）。
func (r *Registry) Close(id uint) {
	now := time.Now()
	_ = r.DB.Model(&model.ConsoleSession{ID: id}).Where("status = ?", "active").Updates(map[string]interface{}{
		"status": "closed", "ended_at": now,
	})
	r.mu.Lock()
	delete(r.conns, id)
	for token, sid := range r.byToken {
		if sid == id {
			delete(r.byToken, token)
		}
	}
	r.mu.Unlock()
}

// Disconnect 强制断开会话：ssh/serial 关闭 WS（处理循环随即退出并走 Close）；
// VNC 无法切断 websockify 侧 TCP，返回 ErrNoLiveConn 由调用方翻译。
func (r *Registry) Disconnect(id uint) error {
	r.mu.Lock()
	conn, ok := r.conns[id]
	r.mu.Unlock()
	if !ok {
		return ErrNoLiveConn
	}
	// 关闭帧与底层关闭都在 Conn 的写锁内完成，与桥接 goroutine 的转发写互斥
	return conn.CloseWithReason("管理员已断开连接")
}

// noLiveConnError 会话无可控连接（VNC 或已关闭）。
type noLiveConnError struct{}

func (noLiveConnError) Error() string {
	return "该会话无可控连接（VNC 中转连接无法强制断开）"
}

// ErrNoLiveConn 会话无可控连接时的哨兵错误（供 handler 识别翻译）。
var ErrNoLiveConn = noLiveConnError{}
