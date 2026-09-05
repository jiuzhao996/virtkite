package console

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// VNC 会话无连接关闭事件：超过该时长无 token 解析即视为掉线，由清扫器收敛。
const vncStaleAfter = 60 * time.Minute

// Registry 控制台会话注册表（单进程内存 + DB 持久）。
// SSH/串口 WS 连接持有在此，可服务端强制断开；VNC token 映射用于解析事件刷新存活。
type Registry struct {
	mu      sync.Mutex
	DB      *gorm.DB
	conns   map[uint]*websocket.Conn // sessionID → WS 连接（仅 ssh/serial）
	byToken map[string]uint          // vnc token → sessionID
}

// NewRegistry 创建会话注册表。
func NewRegistry(db *gorm.DB) *Registry {
	return &Registry{
		DB:      db,
		conns:   make(map[uint]*websocket.Conn),
		byToken: make(map[string]uint),
	}
}

// StartSweeper 启动过期清扫（VNC 无关闭事件，超 vncStaleAfter 未解析即标记 closed）。
func (r *Registry) StartSweeper() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cutoff := time.Now().Add(-vncStaleAfter)
			_ = r.DB.Model(&model.ConsoleSession{}).
				Where("status = ? AND type = ? AND last_seen < ?", "active", "vnc", cutoff).
				Updates(map[string]interface{}{"status": "closed", "ended_at": time.Now()})
			// 清理已关闭会话的 token 映射（避免内存膨胀）
			r.mu.Lock()
			for token, id := range r.byToken {
				var s model.ConsoleSession
				if err := r.DB.Select("status").First(&s, id).Error; err != nil || s.Status != "active" {
					delete(r.byToken, token)
				}
			}
			r.mu.Unlock()
		}
	}()
}

// Open 创建 WS 会话（ssh/serial）并持有连接，返回 DB 记录。
func (r *Registry) Open(vmID uint, vmName, typ, username string, userID *uint, clientIP string, conn *websocket.Conn) *model.ConsoleSession {
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
	// 先发关闭帧再关底层连接，尽量让浏览器收到通知
	_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "管理员已断开连接"))
	return conn.Close()
}

// noLiveConnError 会话无可控连接（VNC 或已关闭）。
type noLiveConnError struct{}

func (noLiveConnError) Error() string {
	return "该会话无可控连接（VNC 中转连接无法强制断开）"
}

// ErrNoLiveConn 会话无可控连接时的哨兵错误（供 handler 识别翻译）。
var ErrNoLiveConn = noLiveConnError{}
