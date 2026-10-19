package jumpd

import (
	"fmt"
	"io"
	"log"
	"net"
	"runtime/debug"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"github.com/jiuzhao/vmops/model"
)

// 本文件是 SSH 跳板服务端的核心：服务启动与监听、单连接握手与角色判定、以及在菜单与
// 桥接之间分发的 accept/menu 循环。目标选择与重验、会话桥接分别见 selection.go / session.go。

// Start 启动 SSH 跳板服务端（非阻塞：内部 goroutine，进程生命周期即服务生命周期）。
// JUMPD_ENABLED 未开启时静默返回（对齐 file-sd「未配置不启动」模式）。
// enabled 由 main.go 传 config.JumpdEnabled；host key 加载失败 fail-closed 直接拒启。
func Start(db *gorm.DB, enabled bool, port int, masterSecret string) {
	if !enabled {
		return
	}
	signer, err := loadOrCreateHostKey(hostKeyPath)
	if err != nil {
		log.Printf("[jumpd] 主机密钥不可用，跳板服务拒绝启动: %v", err)
		return
	}
	a := newAuthenticator(db)
	srv := &Server{DB: db, auth: a, signer: signer, masterSecret: masterSecret}
	go srv.listenAndServe(port) // 启动横幅由 listenAndServe 在 bind 成功后打印（失败时不再误报已启动）
}

// hostKeyPath 跳板主机密钥落盘路径（data/ 已 gitignore，部署侧创建）
const hostKeyPath = "data/jumpd_host_key"

// Server SSH 跳板服务端
type Server struct {
	DB           *gorm.DB
	auth         *authenticator
	signer       ssh.Signer
	masterSecret string
}

// listenAndServe 监听并逐连接处理（单连接单 goroutine，accept 循环不阻塞）。
// 整个循环带 recover 兜底：任何 panic 只进日志，进程不退（项目强制标准第 10 条）。
func (s *Server) listenAndServe(port int) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[jumpd] accept 循环异常退出（已兜底）: %v\n%s", r, debug.Stack())
		}
	}()
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Printf("[jumpd] 监听 :%d 失败（端口被占用？）: %v", port, err)
		return
	}
	log.Printf("[jumpd] SSH 跳板入口已启动 :%d（ssh <用户名>@<宿主机> -p %d）", port, port)
	config := &ssh.ServerConfig{
		// ⚠️ 主机密钥必须 AddHostKey 注册：漏掉时 NewServerConn 立即失败（ssh: server has no host keys），
		// 表现为连接被静默重置——本次实现曾踩中，留注释防回退
		PasswordCallback: func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			ip := meta.RemoteAddr().String()
			if host, _, splitErr := net.SplitHostPort(ip); splitErr == nil {
				ip = host
			}
			u, msg, authErr := s.auth.authenticate(ip, meta.User(), string(pass))
			if authErr != nil {
				log.Printf("[jumpd] 认证拒绝 user=%q ip=%s 原因=%v", meta.User(), ip, authErr)
				return nil, fmt.Errorf("%s", msg)
			}
			// 认证上下文经 Permissions 传递到后续处理（不做二次查询）
			return &ssh.Permissions{
				Extensions: map[string]string{
					"jumpd.uid":      fmt.Sprintf("%d", u.ID),
					"jumpd.username": u.Username,
					"jumpd.role":     u.Role,
				},
			}, nil
		},
	}
	config.AddHostKey(s.signer)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("[jumpd] accept 失败: %v", err)
			continue
		}
		go s.handleConn(conn, config)
	}
}

// enableKeepAlive 尽力开启 TCP 保活：非 TCP 连接（测试假件/管道）静默跳过。
// 客户端掉电/断网形成的半开连接靠它探测（Idle 60s 后每 15s 一次、4 次无应答判死，
// 约 2 分钟内 Read 报错）——否则键流不关、心跳一直喂 last_seen，会话行能「活跃」
// 挂数小时（sweeper 的 last_seen 兜底判据被喂活，形同虚设）。
func enableKeepAlive(conn net.Conn) {
	type keepAliveSetter interface {
		SetKeepAliveConfig(net.KeepAliveConfig) error
	}
	if c, ok := conn.(keepAliveSetter); ok {
		_ = c.SetKeepAliveConfig(net.KeepAliveConfig{
			Enable: true, Idle: 60 * time.Second, Interval: 15 * time.Second, Count: 4,
		})
	}
}

// handleConn 单连接全流程：SSH 握手（含密码认证）→ 菜单循环 → 重验 → 拨号 → IO 桥。
// 目标地址唯一来源是 revalidateSelection 返回的 DB 行——本函数及下游不存在从
// 用户键盘输入解析 IP/主机名的任何通道（计划代码评审硬项）。
func (s *Server) handleConn(conn net.Conn, config *ssh.ServerConfig) {
	defer conn.Close()
	// 每个 SSH 连接一个后台 goroutine，panic 会直接终止整个进程（无 gin Recovery）。
	// 连接处理由远端输入驱动，一个畸形请求不应带走所有人的会话。
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[jumpd] 连接处理 panic from %s（已兜底）: %v\n%s", conn.RemoteAddr(), r, debug.Stack())
		}
	}()
	enableKeepAlive(conn)
	sconn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		// 认证失败已在 PasswordCallback 留痕；其余握手错误（版本协商/KEX 异常）值得记录
		log.Printf("[jumpd] SSH 握手未完成 from %s: %v", conn.RemoteAddr(), err)
		return
	}
	defer sconn.Close()
	clientIP := sconn.RemoteAddr().String()
	if host, _, splitErr := net.SplitHostPort(clientIP); splitErr == nil {
		clientIP = host
	}
	ext := sconn.Permissions.Extensions
	user := &model.User{}
	if uid, parseErr := strconv.ParseUint(ext["jumpd.uid"], 10, 64); parseErr == nil {
		user.ID = uint(uid)
	} else {
		log.Printf("[jumpd] 认证上下文 uid 解析失败（理论不可达，Permissions 为自家构造）: %v", parseErr)
	}
	user.Username = ext["jumpd.username"]
	user.Role = ext["jumpd.role"]

	// 全局请求（keepalive 等）一律不承诺支持，客户端可容忍
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[jumpd] 全局请求循环异常: %v\n%s", r, debug.Stack())
			}
		}()
		for req := range reqs {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}()

	// 认证上下文经 Permissions 传递（握手时已认证，不做二次查询）
	// viewer/未知角色：认证已放行（密码失败无法携带文案），在此会话层拒绝
	if !roleAllowed(user.Role) {
		s.rejectReadOnly(chans)
		return
	}
	isAdmin := user.Role == "admin"
	s.menuLoop(user, isAdmin, clientIP, chans)
}

// rejectReadOnly 只读角色的会话层拒绝：接受通道、等 shell 请求真正到达后再给中文
// 提示并正常退出（原实现固定 sleep 300ms 等 pty 就绪，慢网络下提示会先于终端建立
// 而被丢弃/截断——改为事件驱动，快网络也不白等）
func (s *Server) rejectReadOnly(chans <-chan ssh.NewChannel) {
	select {
	case newCh := <-chans:
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "jumpd 仅支持 session 通道")
			return
		}
		ch, inReqs, err := newCh.Accept()
		if err != nil {
			return
		}
		defer ch.Close()
		shellReady := make(chan struct{}, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[jumpd] 只读拒绝请求循环异常: %v\n%s", r, debug.Stack())
				}
			}()
			for req := range inReqs {
				if req.Type == "pty-req" || req.Type == "shell" {
					_ = req.Reply(true, nil)
					if req.Type == "shell" {
						select {
						case shellReady <- struct{}{}:
						default:
						}
					}
				} else if req.WantReply {
					_ = req.Reply(false, nil)
				}
			}
		}()
		// shell 请求到达 = 客户端 pty 已建立、开始读输出，此时写提示渲染才正常；
		// 迟迟不发 shell 的非交互客户端超时收工（与 runSession 的 10s 口径一致）
		select {
		case <-shellReady:
		case <-time.After(10 * time.Second):
			return
		}
		_, _ = io.WriteString(ch, "\r\n✗ 只读角色不支持终端登录（viewer 无资产终端权限）\r\n")
		sendExitStatus(ch)
	case <-time.After(30 * time.Second):
		// 客户端始终不开会话通道，超时收工
	}
}

// menuLoop 菜单主循环：每个 session channel 一轮菜单；桥接结束后 channel 不关闭、
// 原地重回菜单（对齐 koko 行为：用户 exit 后见菜单，q 才真正断开）。
func (s *Server) menuLoop(user *model.User, isAdmin bool, clientIP string, chans <-chan ssh.NewChannel) {
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "jumpd 仅支持 session 通道")
			continue
		}
		ch, inReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		s.runSession(ch, inReqs, user, isAdmin, clientIP)
	}
}
