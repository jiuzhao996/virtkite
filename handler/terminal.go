package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/console"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

// TerminalHandler Web 终端（WebSocket ↔ SSH 桥）处理器。
// 前端 xterm.js 通过 WS 连接，首个消息携带 SSH 连接参数，后端用 x/crypto/ssh 桥接目标主机。
type TerminalHandler struct {
	DB       *gorm.DB
	Sessions *console.Registry
}

// NewTerminalHandler 创建 Web 终端处理器。
func NewTerminalHandler(db *gorm.DB, sessions *console.Registry) *TerminalHandler {
	return &TerminalHandler{DB: db, Sessions: sessions}
}

// wsUpgrader 不限制来源，由 JWT 中间件保证鉴权。
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Connect 处理 WebSocket SSH 终端连接（GET /api/vms/:id/terminal）。
func (h *TerminalHandler) Connect(c *gin.Context) {
	rawConn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	// stdout/stderr 转发、主循环 pong、管理员强制断开会并发写同一连接，
	// 必须经 console.Conn 串行化，否则 gorilla/websocket 直接 panic 并带走进程。
	conn := console.NewConn(rawConn)
	defer conn.Close()
	log.Printf("[terminal] WS 已连接 from=%s vm=%s", c.ClientIP(), c.Param("id"))

	// 校验 VM 存在（ID 必须先解析成数值，直传字符串会被 GORM 当原始 SQL 拼接，见 param.go）
	vmID, ok := parseID(c.Param("id"))
	if !ok {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "虚拟机 ID 非法"})
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, vmID).Error; err != nil {
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

	// 目标白名单校验：拨号参数完全来自浏览器，不校验等于把平台变成跳板机
	if err := validateSSHTarget(&vm, auth.Host, port); err != nil {
		log.Printf("[terminal] 目标被拒 vm=%s(%d) target=%s:%d user=%s from=%s reason=%v",
			vm.Name, vm.ID, auth.Host, port, auth.User, c.ClientIP(), err)
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": err.Error()})
		return
	}
	// 留痕：谁、从哪、连了哪个目标（口令不记录）
	log.Printf("[terminal] SSH 拨号 vm=%s(%d) target=%s:%d user=%s from=%s",
		vm.Name, vm.ID, auth.Host, port, auth.User, c.ClientIP())

	// SSH 连接目标主机
	sshConfig := &ssh.ClientConfig{
		User: auth.User,
		Auth: []ssh.AuthMethod{ssh.Password(auth.Password)},
		// 目标是平台自己创建的短生命周期虚拟机，IP 由 DHCP 动态分配、重建即换主机密钥，
		// 维护 known_hosts 不具可操作性，故显式跳过主机密钥校验。
		// 中间人风险由上面的 validateSSHTarget 收敛：目标被限制在本机私有网段内。
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         8 * time.Second,
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

	// 记录 SSH 会话并持有 WS 连接（退出时关闭会话；管理员可服务端强制断开）
	if h.Sessions != nil {
		uid, uname := taskUserFromContext(c)
		if s := h.Sessions.Open(vm.ID, vm.Name, "ssh", uname, uid, c.ClientIP(), conn); s != nil {
			defer h.Sessions.Close(s.ID)
		}
	}

	var wg sync.WaitGroup

	// SSH stdout/stderr → WebSocket（二进制帧，xterm 直接写入）
	// 两个 goroutine 并发调用，写入由 console.Conn 的写锁串行化。
	pipeOut := func(r io.Reader) {
		defer wg.Done()
		// 后台 goroutine 的 panic 无法被 gin Recovery 拦截，会直接终止进程，必须自兜底
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[terminal] 输出转发 panic vm=%s panic=%v\n%s", vm.Name, rec, debug.Stack())
			}
		}()
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
readLoop:
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
					// 带标签跳出外层 for：裸 break 只能跳出 switch（原实现的缺陷）
					break readLoop
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
				break readLoop
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

// validateSSHTarget 校验 Web 终端的 SSH 拨号目标。
//
// 原实现里 host/port/user/password 全部取自浏览器首帧且零校验，配合 RBAC 对 GET 的放行，
// 任何登录用户都能驱动服务器向任意地址发起 SSH 连接 —— 平台等于免费的跳板机、
// 内网端口扫描器与口令爆破器。本函数把目标收敛到「本机管理的虚拟机」范围内。
//
// 约束由强到弱：
//  1. 平台已记录该 VM 的 IP（vm.IP 非空）→ 目标必须与之精确一致；
//  2. 未记录 IP（无 guest agent 时的常态）→ 只接受 RFC1918 私有网段的 IP 字面量，
//     并排除环回（否则可 SSH 进宿主机自身）、链路本地、组播与未指定地址；
//     不接受主机名，避免 DNS 解析到公网或 DNS rebinding 绕过；
//  3. 端口必须落在 1-65535。
//
// 返回的错误文案会直接回显到前端终端，因此一律为中文且不含内部细节。
func validateSSHTarget(vm *model.VM, host string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口不合法（须在 1-65535 之间）")
	}

	// 平台已知该虚拟机地址时，只允许连它自己
	if recorded := strings.TrimSpace(vm.IP); recorded != "" {
		if host != recorded {
			return fmt.Errorf("只能连接该虚拟机自身地址 %s", recorded)
		}
		return nil
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("目标必须是 IP 地址（不支持主机名）")
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return fmt.Errorf("该地址不允许作为终端目标（环回 / 链路本地 / 组播）")
	}
	if !ip.IsPrivate() {
		return fmt.Errorf("只允许连接私有网段地址（10/8、172.16/12、192.168/16）")
	}
	return nil
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
