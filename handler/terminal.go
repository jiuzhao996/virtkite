package handler

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/jiuzhao/vmops/model"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

// TerminalHandler Web 终端（WebSocket ↔ SSH 桥）处理器。
// 前端 xterm.js 通过 WS 连接，首个消息携带 SSH 连接参数，后端用 x/crypto/ssh 桥接目标主机。
type TerminalHandler struct {
	DB *gorm.DB
}

// NewTerminalHandler 创建 Web 终端处理器。
func NewTerminalHandler(db *gorm.DB) *TerminalHandler {
	return &TerminalHandler{DB: db}
}

// wsUpgrader 不限制来源，由 JWT 中间件保证鉴权。
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Connect 处理 WebSocket SSH 终端连接（GET /api/vms/:id/terminal）。
func (h *TerminalHandler) Connect(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	log.Printf("[terminal] WS 已连接 from=%s vm=%s", c.ClientIP(), c.Param("id"))

	// 校验 VM 存在
	var vm model.VM
	if err := h.DB.First(&vm, c.Param("id")).Error; err != nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "虚拟机不存在"})
		return
	}

	// 读取首个消息：SSH 连接参数
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var auth struct {
		Type     string `json:"type"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &auth); err != nil || auth.Type != "auth" {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "连接参数缺失"})
		return
	}
	if auth.Host == "" || auth.User == "" || auth.Password == "" {
		log.Printf("[terminal] 参数缺失 host=%q user=%q", auth.Host, auth.User)
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "请填写主机、用户名和密码"})
		return
	}
	port := auth.Port
	if port == 0 {
		port = 22
	}

	// SSH 连接目标主机
	sshConfig := &ssh.ClientConfig{
		User:    auth.User,
		Auth:    []ssh.AuthMethod{ssh.Password(auth.Password)},
		Timeout: 8 * time.Second,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(auth.Host, itoa(port)), sshConfig)
	if err != nil {
		log.Printf("[terminal] SSH 拨号失败 host=%s:%d err=%v", auth.Host, port, err)
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "SSH 连接失败: " + err.Error()})
		return
	}
	defer client.Close()
	log.Printf("[terminal] SSH 已连接 %s:%d@%s", auth.Host, port, auth.User)

	session, err := client.NewSession()
	if err != nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "SSH 会话创建失败"})
		return
	}
	defer session.Close()

	// 申请 PTY，接收 resize 调整窗口
	cols, rows := 80, 24
	if err := session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "申请 PTY 失败: " + err.Error()})
		return
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "获取 stdin 失败"})
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "获取 stdout 失败"})
		return
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "获取 stderr 失败"})
		return
	}

	if err := session.Shell(); err != nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "启动 shell 失败: " + err.Error()})
		return
	}

	_ = conn.WriteJSON(gin.H{"type": "connected", "host": auth.Host})

	var wg sync.WaitGroup

	// SSH stdout/stderr → WebSocket（二进制帧，xterm 直接写入）
	pipeOut := func(r io.Reader) {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
	wg.Add(2)
	go pipeOut(stdout)
	go pipeOut(stderr)

	// WebSocket → SSH stdin，同时处理 resize / 心跳
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if json.Unmarshal(data, &msg) == nil {
			switch msg.Type {
			case "input":
				if _, werr := stdin.Write([]byte(msg.Data)); werr != nil {
					_ = conn.Close()
					break
				}
			case "resize":
				if msg.Cols > 0 && msg.Rows > 0 {
					cols, rows = msg.Cols, msg.Rows
					_ = session.WindowChange(rows, cols)
				}
			case "ping":
				_ = conn.WriteJSON(gin.H{"type": "pong"})
			}
		} else {
			// 兼容直接发送文本输入
			if _, werr := stdin.Write(data); werr != nil {
				break
			}
		}
	}

	// 会话退出信号
	_ = session.Close()
	_ = client.Close()
	_ = conn.Close()
	_ = session.Wait()
	wg.Wait()
	_ = client.Close()
}

// itoa 简易整型转字符串，避免额外 import。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
