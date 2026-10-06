package console

import (
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ErrConnClosed 向已关闭连接写入时返回的哨兵错误。
// 桥接 goroutine 收到该错误即应退出转发循环。
var ErrConnClosed = errors.New("WebSocket 连接已关闭")

// wsWriteTimeout 单帧写超时：对端半死（拔网线 / NAT 静默丢连接）时内核写缓冲
// 排满后无限阻塞，且写方持有 writeMu——一个卡死的写会把所有写方和转发循环
// 一起拖死（goroutine + 连接泄漏）。每帧写前重置 deadline，任何一帧最多挂这么久。
// 控制台流量是交互小帧，10s 足够宽裕。
const wsWriteTimeout = 10 * time.Second

// Conn 串行化写入的 WebSocket 包装。
//
// gorilla/websocket 允许「一个读 goroutine + 一个写 goroutine」并发，
// 但明确禁止多个 goroutine 同时写，违反会直接 panic
// （concurrent write to websocket connection），且 panic 发生在 goroutine 内部，
// gin 的 Recovery 中间件无法拦截 —— 整个服务进程会被带走。
//
// 本项目控制台桥同时存在多路写入方：
//   - SSH 桥：stdout 转发、stderr 转发、主循环的 pong/error 帧（handler/terminal.go）
//   - 串口桥：guest 输出转发、错误帧（handler/serial.go）
//   - 会话注册表：管理员强制断开时发送的 CloseMessage（registry.go 的 Disconnect）
//
// 因此所有写操作必须经本类型的写锁串行化。读侧由单一 goroutine 独占，无需加锁。
type Conn struct {
	ws *websocket.Conn

	writeMu sync.Mutex
	closed  bool // 由 writeMu 保护：关闭后拒绝后续写，避免向已关连接写入
}

// NewConn 包装原始 WebSocket 连接，返回写入串行化的安全连接。
func NewConn(ws *websocket.Conn) *Conn {
	return &Conn{ws: ws}
}

// WriteMessage 串行化写入一帧（对应 websocket.Conn.WriteMessage）。
func (c *Conn) WriteMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return ErrConnClosed
	}
	_ = c.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return c.ws.WriteMessage(messageType, data)
}

// WriteJSON 串行化写入一个 JSON 帧（对应 websocket.Conn.WriteJSON）。
func (c *Conn) WriteJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return ErrConnClosed
	}
	_ = c.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return c.ws.WriteJSON(v)
}

// ReadMessage 读取一帧（对应 websocket.Conn.ReadMessage）。
// 读侧由单一 goroutine 独占故不加锁；连接被关闭后本调用返回错误，转发循环随即退出。
func (c *Conn) ReadMessage() (messageType int, p []byte, err error) {
	return c.ws.ReadMessage()
}

// Close 关闭底层连接（幂等）。关闭后所有写入返回 ErrConnClosed。
func (c *Conn) Close() error {
	return c.CloseWithReason("")
}

// CloseWithReason 先尽力发送关闭帧（让浏览器收到中文提示）再关闭底层连接，幂等。
// 用于管理员强制断开等需要告知对端原因的场景。
func (c *Conn) CloseWithReason(reason string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if reason != "" {
		// 关闭帧同样受写超时保护（对端半死时不让管理员断开操作挂死）；
		// 发送失败不影响后续强制关闭，忽略错误
		_ = c.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		_ = c.ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason))
	}
	// ws.Close 只关闭底层 fd、立即返回（默认无 SO_LINGER），持锁调用无害；
	// 它会让所有阻塞中的 Read/Write 立即失败，正是各转发循环需要的退出信号
	return c.ws.Close()
}
