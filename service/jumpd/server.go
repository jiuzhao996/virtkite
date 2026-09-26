package jumpd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/vmssh"
)

// jumpTarget 重验通过后的连接目标：所有字段都来自 DB 行（用户键盘输入不参与）
type jumpTarget struct {
	VM       model.VM
	User     string
	Port     int
	Password string // 明文，仅内存流转，禁止进日志/落库
}

// loadMenuAssets 查询某用户可见的菜单资产：running ∩ 有 IP ∩ 有托管凭据（∩ 有效授权，admin 豁免）。
// 授权「未过期」判定与 handler/vm_grant.go:45 逐字一致（fail-closed：查失败按空授权处理）。
func loadMenuAssets(db *gorm.DB, user *model.User, isAdmin bool) ([]menuVM, error) {
	var vms []model.VM
	if err := db.Where("status = ? AND ip <> ''", model.VMStatusRunning).Find(&vms).Error; err != nil {
		return nil, fmt.Errorf("查询虚拟机列表失败: %w", err)
	}

	authSet := map[uint]bool{}
	if !isAdmin {
		var ids []uint
		// 与 handler/vm_grant.go:45 的未过期条件逐字一致
		if err := db.Model(&model.VMGrant{}).
			Where("user_id = ? AND (expires_at IS NULL OR expires_at > ?)", user.ID, time.Now()).
			Pluck("vm_id", &ids).Error; err != nil {
			return nil, fmt.Errorf("查询授权失败: %w", err)
		}
		for _, id := range ids {
			authSet[id] = true
		}
	}

	var credIDs []uint
	if err := db.Model(&model.VMCredential{}).Pluck("vm_id", &credIDs).Error; err != nil {
		return nil, fmt.Errorf("查询托管凭据失败: %w", err)
	}
	credSet := map[uint]bool{}
	for _, id := range credIDs {
		credSet[id] = true
	}

	return filterMenuAssets(vms, authSet, credSet, isAdmin), nil
}

// revalidateSelection 连接前的现查重验（Review Focus #1/#2）：菜单展示到按键选择之间存在
// 时间窗，授权可能被收回、VM 可能关机、IP 可能清空——一切以当前 DB 行为准，绝不信任菜单缓存。
func revalidateSelection(db *gorm.DB, user *model.User, vmID uint, isAdmin bool, masterSecret string) (*jumpTarget, error) {
	var vm model.VM
	if err := db.First(&vm, vmID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("虚拟机不存在或已删除")
		}
		log.Printf("[jumpd] 重验查库失败，fail-closed 拒绝: %v", err)
		return nil, fmt.Errorf("服务暂时不可用，请稍后再试")
	}
	if vm.Status != model.VMStatusRunning {
		return nil, fmt.Errorf("虚拟机不在运行中，无法连接")
	}
	if vm.IP == "" {
		return nil, fmt.Errorf("虚拟机尚未获取 IP，无法连接")
	}

	if !isAdmin {
		var cnt int64
		if err := db.Model(&model.VMGrant{}).
			Where("user_id = ? AND vm_id = ? AND (expires_at IS NULL OR expires_at > ?)", user.ID, vmID, time.Now()).
			Count(&cnt).Error; err != nil {
			log.Printf("[jumpd] 重验授权查库失败，fail-closed 拒绝: %v", err)
			return nil, fmt.Errorf("授权校验失败，请稍后再试")
		}
		if cnt == 0 {
			return nil, fmt.Errorf("你已无该虚拟机的连接授权")
		}
	}

	var cred model.VMCredential
	if err := db.Where("vm_id = ?", vmID).First(&cred).Error; err != nil {
		return nil, fmt.Errorf("该虚拟机未托管 SSH 凭据，请先在 Web 端凭据面板保存")
	}
	plain, err := secretbox.OpenWithMaster(masterSecret, cred.Salt, cred.PasswordEnc)
	if err != nil {
		log.Printf("[jumpd] 凭据解密失败 vm=%d（主密钥是否轮换过？）: %v", vmID, err)
		return nil, fmt.Errorf("凭据解密失败，请在 Web 端重新保存该虚拟机的 SSH 凭据")
	}

	return &jumpTarget{VM: vm, User: cred.User, Port: cred.Port, Password: string(plain)}, nil
}

// 会话状态与类型取值与 model/console_sessions 现有风格一致（active/closed；type 增加 jump）
const (
	sessionTypeJump = "jump"
	sessionActive   = "active"
	sessionClosed   = "closed"
)

// openJumpSession 跳板会话登记（先例 registry.go OpenVNC 的直建行模式，不经 websocket 注册表）。
// 创建失败只留痕不阻断——审计缺失不应阻止合法连接，但必须有迹可查。
func openJumpSession(db *gorm.DB, vmID uint, vmName string, userID *uint, username, clientIP string) *model.ConsoleSession {
	now := time.Now()
	s := &model.ConsoleSession{
		VMID: vmID, VMName: vmName, Type: sessionTypeJump,
		UserID: userID, Username: username, ClientIP: clientIP,
		Status: sessionActive, StartedAt: now, LastSeen: now,
	}
	if err := db.Create(s).Error; err != nil {
		log.Printf("[jumpd] 会话登记失败 vm=%s user=%s err=%v", vmName, username, err)
		return nil
	}
	return s
}

// closeJumpSession 会话关闭（先例 registry.go Close 的 dbx 收口模式）。
// 幂等：只改 active 行，二次调用 0 行命中即无事发生。
func closeJumpSession(db *gorm.DB, id uint, reason string) {
	if id == 0 {
		return
	}
	dbx.PersistBestEffort(db, "jumpd-session-close", func() error {
		now := time.Now()
		return db.Model(&model.ConsoleSession{}).
			Where("id = ? AND status = ?", id, sessionActive).
			Updates(map[string]interface{}{"status": sessionClosed, "ended_at": &now}).Error
	})
	log.Printf("[jumpd] 会话 %d 关闭（%s）", id, reason)
}

// safeCopy 桥接拷贝的 recover 包装：跑在 goroutine 里，panic 一旦外泄会带走整个进程
// （gin Recovery 管不到自起 goroutine，项目强制标准第 10 条）。错误与 panic 只留痕，
// 用户可见侧以连接关闭体现，细节不外泄。
func safeCopy(dst io.Writer, src io.Reader, sessionID uint) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[jumpd] 会话 %d 桥接异常（已兜底）: %v\n%s", sessionID, r, debug.Stack())
		}
	}()
	if _, err := io.Copy(dst, src); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("[jumpd] 会话 %d 桥接拷贝结束: %v", sessionID, err)
	}
}

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
	a := &authenticator{DB: db, limiter: newLimiter(time.Minute, 5)}
	srv := &Server{DB: db, auth: a, signer: signer, masterSecret: masterSecret}
	go srv.listenAndServe(port)
	log.Printf("[jumpd] SSH 跳板入口已启动 :%d（ssh <用户名>@<宿主机> -p %d）", port, port)
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

// handleConn 单连接全流程：SSH 握手（含密码认证）→ 菜单循环 → 重验 → 拨号 → IO 桥。
// 目标地址唯一来源是 revalidateSelection 返回的 DB 行——本函数及下游不存在从
// 用户键盘输入解析 IP/主机名的任何通道（计划代码评审硬项）。
func (s *Server) handleConn(conn net.Conn, config *ssh.ServerConfig) {
	defer conn.Close()
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
				req.Reply(false, nil)
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

// rejectReadOnly 只读角色的会话层拒绝：接受通道、等 shell 请求就绪后给出中文提示并正常退出
func (s *Server) rejectReadOnly(chans <-chan ssh.NewChannel) {
	select {
	case newCh := <-chans:
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "jumpd 仅支持 session 通道")
			return
		}
		ch, inReqs, err := newCh.Accept()
		if err != nil {
			return
		}
		defer ch.Close()
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[jumpd] 只读拒绝请求循环异常: %v\n%s", r, debug.Stack())
				}
			}()
			for req := range inReqs {
				if req.Type == "pty-req" || req.Type == "shell" {
					req.Reply(true, nil)
				} else if req.WantReply {
					req.Reply(false, nil)
				}
			}
		}()
		time.Sleep(300 * time.Millisecond) // 等客户端完成 pty/shell 请求、终端就绪再写提示
		io.WriteString(ch, "\r\n✗ 只读角色不支持终端登录（viewer 无资产终端权限）\r\n")
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
			newCh.Reject(ssh.UnknownChannelType, "jumpd 仅支持 session 通道")
			continue
		}
		ch, inReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		s.runSession(ch, inReqs, user, isAdmin, clientIP)
	}
}

// keyStream 通道的单读者包装：整个连接里只有这个 goroutine 读 ch，
// 菜单阶段消费 keyStream、桥接阶段由 forwardKeys 消费——杜绝两个读者抢同一 channel
// （终审 I-2：原 safeCopy(stdin, ch) 在桥接结束后阻塞残留，与菜单抢按键）
type keyStream struct {
	ch chan byte
}

func startKeyReader(ch ssh.Channel) *keyStream {
	ks := &keyStream{ch: make(chan byte, 128)}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[jumpd] 键流读取异常: %v\n%s", r, debug.Stack())
			}
			close(ks.ch)
		}()
		buf := make([]byte, 256)
		for {
			n, err := ch.Read(buf)
			for i := 0; i < n; i++ {
				ks.ch <- buf[i]
			}
			if err != nil {
				return
			}
		}
	}()
	return ks
}

// forwardKeys 桥接期按键转发：ks → 目标 stdin；桥接结束（waitDone 关闭）即停，
// 停止后未消费的字节留在 ks 里归菜单读取
// onBlockedFn 命令拦截回调（bridgeSession 注入）：落审计 + 写用户提示。
// 独立成参数以便单测（不依赖 DB / channel）。
type onBlockedFn func(line string)

func forwardKeys(ks *keyStream, stdin io.Writer, waitDone <-chan struct{}, onBlocked onBlockedFn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[jumpd] 按键转发异常: %v\n%s", r, debug.Stack())
		}
	}()
	blacklist := currentBlacklist()
	var line []byte // 当前行输入缓冲（仅用户侧字节，与目标输出无关）
	for {
		select {
		case b, ok := <-ks.ch:
			if !ok {
				return
			}
			if b == '\r' || b == '\n' {
				// 行结束：先判黑名单再决定放行
				lineStr := strings.TrimSpace(string(line))
				if matchBlacklist(lineStr, blacklist) {
					// 拦截整行：目标机 tty 里已回显的命令用 Ctrl-U（0x15）清行，
					// 防止用户下一条命令的回车把残留行误执行；本地缓冲清零
					if _, wErr := stdin.Write([]byte{0x15, '\r'}); wErr != nil {
						return
					}
					io.WriteString(stdin, "\r\n")
					if onBlocked != nil {
						onBlocked(lineStr)
					}
				} else {
					// 正常放行：补发行结束符（逐字节已透传，这里只送回车）
					if _, wErr := stdin.Write([]byte{b}); wErr != nil {
						return
					}
				}
				line = line[:0]
				continue
			}
			// 非行结束字节照常透传（vim/htop 等全屏交互不受影响）
			line = append(line, b)
			if _, err := stdin.Write([]byte{b}); err != nil {
				return
			}
		case <-waitDone:
			return
		}
	}
}

// runSession 单 channel 的「菜单 ⇄ 桥接」循环
func (s *Server) runSession(ch ssh.Channel, inReqs <-chan *ssh.Request, user *model.User, isAdmin bool, clientIP string) {
	defer ch.Close()
	ks := startKeyReader(ch)

	var term = "xterm-256color"
	var termW, termH uint32 = 80, 24
	var activeMu sync.Mutex
	var activeTS *ssh.Session

	// channel 内请求：pty-req 记录终端参数（桥接时原样回放）；shell 放行；
	// exec 拒绝（跳板仅交互式）；window-change 转发给桥接中的目标会话
	shellReady := make(chan struct{}, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[jumpd] 请求循环异常: %v\n%s", r, debug.Stack())
			}
		}()
		for req := range inReqs {
			switch req.Type {
			case "pty-req":
				term, termW, termH = parsePTYReq(req.Payload)
				req.Reply(true, nil)
			case "shell":
				req.Reply(true, nil)
				select {
				case shellReady <- struct{}{}:
				default:
				}
			case "exec":
				req.Reply(false, nil)
			case "window-change":
				if w, h, ok := parseWindowChange(req.Payload); ok {
					activeMu.Lock()
					if activeTS != nil {
						activeTS.WindowChange(int(w), int(h))
					}
					activeMu.Unlock()
				}
				req.Reply(true, nil)
			default:
				if req.WantReply {
					req.Reply(false, nil)
				}
			}
		}
	}()

	select {
	case <-shellReady:
	case <-time.After(10 * time.Second):
		return // 客户端迟迟不发 shell 请求，收工
	}

	page := 1
	for {
		items, err := loadMenuAssets(s.DB, user, isAdmin)
		if err != nil {
			log.Printf("[jumpd] 菜单查询失败 user=%s: %v", user.Username, err)
			io.WriteString(ch, "\r\n✗ 资产查询失败，请稍后重试\r\n")
			sendExitStatus(ch)
			return
		}
		shown, pages := menuPage(items, page)
		io.WriteString(ch, renderMenu(items, page, pages))

		// 注：ssh.Channel 接口无读超时（SetReadDeadline 不存在），菜单空闲不设超时——
		// 空闲连接由客户端侧断开或部署层（防火墙/代理空闲回收）兜底。
		var key [1]byte
		b, ok := <-ks.ch
		if !ok {
			return // 连接断开（键流随 channel 关闭而关闭）
		}
		key[0] = b

		act := handleMenuKey(key[0], page, pages, shown)
		switch act.kind {
		case "quit":
			sendExitStatus(ch)
			return
		case "next":
			page = act.page
		case "select":
			log.Printf("[jumpd] %s(%s) 选中 VM#%d，开始重验与连接", user.Username, clientIP, act.vmID)
			s.bridgeSession(ch, ks, user, isAdmin, clientIP, act.vmID, term, int(termW), int(termH), &activeMu, &activeTS)
			// 桥接结束（用户在目标机 exit / 连接断开）→ 原地重回菜单
		default:
			// stay：无效键，重显菜单
		}
	}
}

// bridgeSession 重验 → 解密 → vmssh 拨号 → PTY 回放 → 双向桥。
// 连接前必须现查重验（Review Focus #1/#2：菜单展示后授权/状态/IP 都可能已变）。
func (s *Server) bridgeSession(ch ssh.Channel, ks *keyStream, user *model.User, isAdmin bool, clientIP string, vmID uint, term string, w, h int, activeMu *sync.Mutex, activeTS **ssh.Session) {
	tgt, err := revalidateSelection(s.DB, user, vmID, isAdmin, s.masterSecret)
	if err != nil {
		io.WriteString(ch, "\r\n✗ "+err.Error()+"，已回到菜单\r\n")
		return
	}
	sess := openJumpSession(s.DB, tgt.VM.ID, tgt.VM.Name, &user.ID, user.Username, clientIP)
	sid := uint(0)
	if sess != nil {
		sid = sess.ID
	}

	opts, optErr := vmssh.NewOptions(tgt.VM.IP, tgt.VM.IP, tgt.Port, tgt.User, tgt.Password)
	if optErr != nil {
		log.Printf("[jumpd] 目标校验失败 vm=%s: %v", tgt.VM.Name, optErr)
		io.WriteString(ch, "\r\n✗ 连接目标校验失败，已回到菜单\r\n")
		closeJumpSession(s.DB, sid, "目标校验失败")
		return
	}
	client, dialErr := vmssh.Dial(opts)
	if dialErr != nil {
		// 指纹不匹配单独提示（对齐 terminal.go 惯例）；其余给固定文案不泄内网拓扑
		if errors.Is(dialErr, vmssh.ErrHostKeyMismatch) {
			io.WriteString(ch, "\r\n✗ 目标主机指纹与首次记录不一致，已拒绝连接；如确属主机重装，请管理员在设置页删除该主机密钥记录后重试\r\n")
		} else {
			io.WriteString(ch, "\r\n✗ 连接虚拟机失败，请确认其 SSH 服务可达\r\n")
		}
		log.Printf("[jumpd] 拨号失败 vm=%s user=%s: %v", tgt.VM.Name, user.Username, dialErr)
		closeJumpSession(s.DB, sid, "拨号失败")
		return
	}
	defer client.Close()

	ts, err := client.NewSession()
	if err != nil {
		io.WriteString(ch, "\r\n✗ 建立目标会话失败，已回到菜单\r\n")
		closeJumpSession(s.DB, sid, "建会话失败")
		return
	}
	defer ts.Close()
	activeMu.Lock()
	*activeTS = ts
	activeMu.Unlock()
	defer func() {
		activeMu.Lock()
		*activeTS = nil
		activeMu.Unlock()
	}()

	// ⚠️ 管道必须在 RequestPty/Shell 之前取：Session 启动后 StdinPipe 会报
	// 「ssh: StdinPipe after process started」（handler/terminal.go 同序）
	stdin, err := ts.StdinPipe()
	if err != nil {
		closeJumpSession(s.DB, sid, "stdin 失败")
		return
	}
	stdout, err := ts.StdoutPipe()
	if err != nil {
		closeJumpSession(s.DB, sid, "stdout 失败")
		return
	}
	stderr, stderrErr := ts.StderrPipe()
	if stderrErr != nil {
		// stderr 透传缺失只影响诊断信息，会话可继续：留痕而非失败（终审 Minor-1）
		log.Printf("[jumpd] 会话 %d stderr 管道获取失败（继续无 stderr 透传）: %v", sid, stderrErr)
	}

	// 回放用户侧终端类型与窗口尺寸（window-change 已持续转发）
	if err := ts.RequestPty(term, h, w, nil); err != nil {
		io.WriteString(ch, "\r\n✗ 申请伪终端失败，已回到菜单\r\n")
		closeJumpSession(s.DB, sid, "PTY 失败")
		return
	}
	if err := ts.Shell(); err != nil {
		io.WriteString(ch, "\r\n✗ 启动目标 shell 失败，已回到菜单\r\n")
		closeJumpSession(s.DB, sid, "shell 失败")
		return
	}

	// 目标会话结束信号（用户 exit / 目标侧断开）
	waitDone := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[jumpd] 会话等待异常: %v\n%s", r, debug.Stack())
			}
			close(waitDone)
		}()
		_ = ts.Wait()
	}()

	// 双向桥：VM→用户走 safeCopy（ch 写侧并发安全）；用户→VM 走 forwardKeys——
	// 单读者模型（终审 I-2）：ch 的读者只有 keyStream 一个，桥接期由 forwardKeys
	// 消费，结束后未消费字节归还菜单；绝不 safeCopy(stdin, ch) 再开第二个读者
	go func() { safeCopy(ch, stdout, sid) }()
	if stderr != nil {
		go func() { safeCopy(ch.Stderr(), stderr, sid) }()
	}
	forwardDone := make(chan struct{})
	go func() {
		onBlocked := func(line string) {
			// 审计落行：Action=jumpd.cmd_blocked，审计中心操作日志 tab 直接可见
			now := time.Now()
			audit := model.AuditLog{
				UserID: &user.ID, Username: user.Username,
				Action: "jumpd.cmd_blocked", ObjectType: "vm", ObjectID: &vmID,
				Detail:   "拦截高危命令: " + line,
				SourceIP: clientIP, Status: "blocked", CreatedAt: now,
			}
			dbx.PersistBestEffort(s.DB, "jumpd-cmd-blocked", func() error {
				return s.DB.Create(&audit).Error
			})
			log.Printf("[jumpd] 已拦截高危命令 user=%s vm=%d cmd=%q", user.Username, vmID, line)
			// 用户提示（写在目标 tty 流里，拦谁都看得见）
			io.WriteString(ch, "\r\n\033[31m✗ 危险命令已被安全策略拦截并审计：\033[0m"+line+"\r\n")
		}
		forwardKeys(ks, stdin, waitDone, onBlocked)
		close(forwardDone)
	}()

	select {
	case <-waitDone: // 主路径：用户在目标机 exit / 目标侧断开
	case <-forwardDone: // 用户侧连接断开（键流关闭）
	}

	io.WriteString(ch, "\r\n\r\n[目标会话已结束，回到菜单]\r\n")
	closeJumpSession(s.DB, sid, "会话结束")
}

// sendExitStatus 以 0 状态关闭 channel，客户端正常退出
func sendExitStatus(ch ssh.Channel) {
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
}

// parsePTYReq 解析 pty-req 载荷：term 字符串 + 宽高（协议顺序 term/w/h/pixw/pixh/modes）
func parsePTYReq(payload []byte) (term string, w, h uint32) {
	if len(payload) < 5 {
		return "xterm-256color", 80, 24
	}
	tl := binary.BigEndian.Uint32(payload[:4])
	// 守卫 +12：term(tl) 之后必须完整放得下 w+h 共 8 字节（终审 I-1：+9 会让 h 越界读 panic）
	if int(tl)+12 > len(payload) {
		return "xterm-256color", 80, 24
	}
	term = string(payload[4 : 4+tl])
	off := 4 + tl
	w = binary.BigEndian.Uint32(payload[off:])
	h = binary.BigEndian.Uint32(payload[off+4:])
	return term, w, h
}

// parseWindowChange 解析 window-change 载荷（w/h/pixw/pixh 四个 uint32）
func parseWindowChange(payload []byte) (w, h uint32, ok bool) {
	if len(payload) < 8 {
		return 0, 0, false
	}
	w = binary.BigEndian.Uint32(payload[:4])
	h = binary.BigEndian.Uint32(payload[4:8])
	return w, h, true
}
