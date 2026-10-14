package jumpd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
	"github.com/jiuzhao/vmops/service/vmssh"
)

// 本文件负责单个 session channel 的「菜单 ⇄ 桥接」循环、以及连接目标机后的双向 IO 桥：
// 键流单读者、按键转发与黑名单拦截、心跳与目标侧探活、强断登记、SSH 协议载荷解析。

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
// onLineFn 行完成回调（bridgeSession 注入）：每条非空完整行回调一次（放行与拦截都回），
// 供全量命令审计与拦截提示。独立成参数以便单测（不依赖 DB / channel）。
type onLineFn func(line string, blocked bool)

// maxLineBuf 当前行缓冲上限：粘贴巨块且无换行时的内存护栏（继续透传，只停止累积）
const maxLineBuf = 4096

func forwardKeys(ks *keyStream, stdin io.Writer, waitDone <-chan struct{}, onLine onLineFn) {
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
				// 行结束：先判黑名单再决定放行；非空行回调（全量命令审计）
				lineStr := strings.TrimSpace(string(line))
				blocked := matchBlacklist(lineStr, blacklist)
				if lineStr != "" && onLine != nil {
					onLine(lineStr, blocked)
				}
				if blocked {
					// 拦截整行：目标机 tty 里已回显的命令用 Ctrl-U（0x15）清行，
					// 防止用户下一条命令的回车把残留行误执行；本地缓冲清零
					if _, wErr := stdin.Write([]byte{0x15, '\r'}); wErr != nil {
						return
					}
					_, _ = io.WriteString(stdin, "\r\n")
				} else {
					// 正常放行：补发行结束符（逐字节已透传，这里只送回车）
					if _, wErr := stdin.Write([]byte{b}); wErr != nil {
						return
					}
				}
				line = line[:0]
				continue
			}
			// 非行结束字节照常透传（vim/htop 等全屏交互不受影响）。
			// 行编辑控制字节必须同步维护本地缓冲，使其与目标 tty 行编辑后的「逻辑行」
			// 一致——否则攻击者可输入「rmX⌫ -rf /」：本地缓冲是 rmx\x7f -rf /（不含
			// 黑名单词，放行），目标 shell 行编辑删掉 X 后实际执行 rm -rf /（旁路绕过）
			switch b {
			case 0x7f, 0x08: // 退格：删前一字符（与 tty canonical 模式一致）
				if len(line) > 0 {
					line = line[:len(line)-1]
				}
			case 0x15: // Ctrl-U：清当前行
				line = line[:0]
			case 0x17: // Ctrl-W：删前一个词（先剥尾部空白再删到词首）
				for len(line) > 0 && line[len(line)-1] == ' ' {
					line = line[:len(line)-1]
				}
				for len(line) > 0 && line[len(line)-1] != ' ' {
					line = line[:len(line)-1]
				}
			default:
				if len(line) < maxLineBuf {
					line = append(line, b)
				}
			}
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
				_ = req.Reply(true, nil)
			case "shell":
				_ = req.Reply(true, nil)
				select {
				case shellReady <- struct{}{}:
				default:
				}
			case "exec":
				_ = req.Reply(false, nil)
			case "window-change":
				if w, h, ok := parseWindowChange(req.Payload); ok {
					activeMu.Lock()
					if activeTS != nil {
						_ = activeTS.WindowChange(int(w), int(h))
					}
					activeMu.Unlock()
				}
				_ = req.Reply(true, nil)
			default:
				if req.WantReply {
					_ = req.Reply(false, nil)
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
	keyword := ""      // 搜索词（koko 交互：/ 进入，回车提交；Esc 清词回全量）
	var items []menuVM // 菜单数据缓存：搜索/翻页/Esc 只重渲染，不重复查库（回车闪刷新的根因）
	refreshItems := func() error {
		var err error
		items, err = loadMenuAssets(s.DB, user, isAdmin)
		return err
	}
	if err := refreshItems(); err != nil {
		log.Printf("[jumpd] 菜单查询失败 user=%s: %v", user.Username, err)
		_, _ = io.WriteString(ch, "\r\n✗ 资产查询失败，请稍后重试\r\n")
		sendExitStatus(ch)
		return
	}
	draw := func() { // 重渲染：搜索过滤与翻页都在缓存上做，无查库无闪屏
		view := items
		if keyword != "" {
			kw := strings.ToLower(keyword)
			view = nil
			for _, it := range items {
				if strings.Contains(strings.ToLower(it.Name), kw) || strings.Contains(strings.ToLower(it.IP), kw) {
					view = append(view, it)
				}
			}
			page = 1
		}
		_, pages := menuPage(view, page)
		_, _ = io.WriteString(ch, "\r\n\r\n"+renderMenu(view, page, pages, keyword))
	}
	draw()

	for {
		// 注：ssh.Channel 接口无读超时（SetReadDeadline 不存在），菜单空闲不设超时——
		// 空闲连接由客户端侧断开或部署层（防火墙/代理空闲回收）兜底。
		var key [1]byte
		b, ok := <-ks.ch
		if !ok {
			return // 连接断开（键流随 channel 关闭而关闭）
		}
		key[0] = b
		// 方向键/F 键为多字节 ESC 序列（ESC [ A/B/C/D…）：吞掉序列剩余字节，
		// 只保留首个 ESC 当 stay，避免 [、A 被当独立键触发多次菜单重绘（deferred minor）
		if key[0] == 0x1b {
			drainEscSeq(ks)
		}

		// '/' 进入搜索：行式读取（回车提交；提交后仅重渲染不查库）
		if key[0] == '/' {
			_, _ = io.WriteString(ch, "\r\n搜索: ")
			line, rerr := readLine(ks, ch)
			if rerr != nil {
				return
			}
			keyword = strings.TrimSpace(line)
			page = 1
			draw()
			continue
		}

		// Esc：清搜索回全量（仅重渲染）
		if key[0] == 0x1b {
			if keyword != "" {
				keyword = ""
				page = 1
				draw()
			}
			continue
		}

		shown, pages := menuPage(viewOf(items, keyword), page)
		act := handleMenuKey(key[0], page, pages, shown)
		switch act.kind {
		case "quit":
			sendExitStatus(ch)
			return
		case "next", "prev":
			page = act.page
			draw()
		case "select":
			log.Printf("[jumpd] %s(%s) 选中 VM#%d，开始重验与连接", user.Username, clientIP, act.vmID)
			s.bridgeSession(ch, ks, user, isAdmin, clientIP, act.vmID, term, int(termW), int(termH), &activeMu, &activeTS)
			// 桥接结束（用户在目标机 exit / 连接断开）→ 原地重回菜单
			// 资产状态可能已变（开关机），重新查库并重绘
			if rerr := refreshItems(); rerr != nil {
				log.Printf("[jumpd] 菜单刷新失败 user=%s: %v", user.Username, rerr)
			}
			draw()
		default:
			// stay：无效键，重显菜单
		}
	}
}

// viewOf 按搜索词过滤缓存视图（keyword 为空返回原集合）
func viewOf(items []menuVM, keyword string) []menuVM {
	if keyword == "" {
		return items
	}
	kw := strings.ToLower(keyword)
	out := make([]menuVM, 0, len(items))
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Name), kw) || strings.Contains(strings.ToLower(it.IP), kw) {
			out = append(out, it)
		}
	}
	return out
}

// bridgeSession 重验 → 解密 → vmssh 拨号 → PTY 回放 → 双向桥。
// 连接前必须现查重验（Review Focus #1/#2：菜单展示后授权/状态/IP 都可能已变）。
func (s *Server) bridgeSession(ch ssh.Channel, ks *keyStream, user *model.User, isAdmin bool, clientIP string, vmID uint, term string, w, h int, activeMu *sync.Mutex, activeTS **ssh.Session) {
	tgt, err := revalidateSelection(s.DB, user, vmID, isAdmin, s.masterSecret)
	if err != nil {
		_, _ = io.WriteString(ch, "\r\n✗ "+err.Error()+"，已回到菜单\r\n")
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
		_, _ = io.WriteString(ch, "\r\n✗ 连接目标校验失败，已回到菜单\r\n")
		closeJumpSession(s.DB, sid, "目标校验失败")
		return
	}
	client, dialErr := vmssh.Dial(opts)
	if dialErr != nil {
		// 指纹不匹配单独提示（对齐 terminal.go 惯例）；其余给固定文案不泄内网拓扑
		if errors.Is(dialErr, vmssh.ErrHostKeyMismatch) {
			_, _ = io.WriteString(ch, "\r\n✗ 目标主机指纹与首次记录不一致，已拒绝连接；如确属主机重装，请管理员在设置页删除该主机密钥记录后重试\r\n")
		} else {
			_, _ = io.WriteString(ch, "\r\n✗ 连接虚拟机失败，请确认其 SSH 服务可达\r\n")
		}
		log.Printf("[jumpd] 拨号失败 vm=%s user=%s: %v", tgt.VM.Name, user.Username, dialErr)
		closeJumpSession(s.DB, sid, "拨号失败")
		return
	}
	defer client.Close()

	ts, err := client.NewSession()
	if err != nil {
		_, _ = io.WriteString(ch, "\r\n✗ 建立目标会话失败，已回到菜单\r\n")
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
		_, _ = io.WriteString(ch, "\r\n✗ 申请伪终端失败，已回到菜单\r\n")
		closeJumpSession(s.DB, sid, "PTY 失败")
		return
	}
	if err := ts.Shell(); err != nil {
		_, _ = io.WriteString(ch, "\r\n✗ 启动目标 shell 失败，已回到菜单\r\n")
		closeJumpSession(s.DB, sid, "shell 失败")
		return
	}

	// 强断登记：Web 端「强制断开」经 liveSessions 找到这里（会话行 ID → kill 钩子）。
	// kill 同时关目标客户端与用户 channel——waitDone/键流双路随即就绪，整条桥按正常
	// 拆链路径收敛（会话行照常收口），用户侧表现为连接被服务端关闭
	unregisterLive := registerLive(sid, func() {
		_ = client.Close()
		_ = ch.Close()
	})
	defer unregisterLive()

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
	// last_seen 心跳 + 目标侧探活：60s 一次（bridge 结束即停）。
	// 先探活再刷 last_seen——探测失败（目标半开：VM 挂起/网段变更/宿主机路由消失）
	// 说明桥已名存实亡，主动关客户端收桥；不探则心跳空转喂 last_seen， sweeper
	// 永远收敛不了一条输出早已断流的死会话（客户端侧的半开由 enableKeepAlive 兜）
	heartbeatStop := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[jumpd] 会话心跳异常: %v\n%s", r, debug.Stack())
			}
		}()
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if _, _, kerr := client.SendRequest("keepalive@openssh.com", true, nil); kerr != nil {
					log.Printf("[jumpd] 会话 %d 目标侧探活失败，主动收桥: %v", sid, kerr)
					_ = client.Close()
					return
				}
				if sid != 0 {
					dbx.PersistBestEffort(s.DB, "jumpd-session-touch", func() error {
						return s.DB.Model(&model.ConsoleSession{}).
							Where("id = ? AND status = ?", sid, sessionActive).
							Update("last_seen", time.Now()).Error
					})
				}
			case <-heartbeatStop:
				return
			}
		}
	}()
	defer close(heartbeatStop)
	if stderr != nil {
		go func() { safeCopy(ch.Stderr(), stderr, sid) }()
	}
	forwardDone := make(chan struct{})
	go func() {
		onLine := func(line string, blocked bool) {
			// 全量命令审计（JumpServer 核心能力的教学版）：放行行也落审计——
			// 事后可查「谁在这台机器上跑过什么」；黑名单只是事前护栏（防误操作），
			// 追责靠的是这里的全量留痕。审计行截断（粘贴巨块不撑爆 Detail）
			if r := []rune(line); len(r) > 512 {
				line = string(r[:512]) + "…(截断)"
			}
			action, status, detail := "jumpd.cmd", "ok", "执行命令: "+line
			if blocked {
				action, status, detail = "jumpd.cmd_blocked", "blocked", "拦截高危命令: "+line
			}
			audit := model.AuditLog{
				UserID: &user.ID, Username: user.Username,
				Action: action, ObjectType: "vm", ObjectID: &vmID,
				Detail:   detail,
				SourceIP: clientIP, Status: status, CreatedAt: time.Now(),
			}
			dbx.PersistBestEffort(s.DB, "jumpd-cmd-audit", func() error {
				return s.DB.Create(&audit).Error
			})
			if blocked {
				log.Printf("[jumpd] 已拦截高危命令 user=%s vm=%d cmd=%q", user.Username, vmID, line)
				// 用户提示（写在目标 tty 流里，拦谁都看得见）
				_, _ = io.WriteString(ch, "\r\n\033[31m✗ 危险命令已被安全策略拦截并审计：\033[0m"+line+"\r\n")
			}
		}
		forwardKeys(ks, stdin, waitDone, onLine)
		close(forwardDone)
	}()

	select {
	case <-waitDone: // 主路径：用户在目标机 exit / 目标侧断开
	case <-forwardDone: // 用户侧连接断开（键流关闭）
	}

	_, _ = io.WriteString(ch, "\r\n\r\n[目标会话已结束，回到菜单]\r\n")
	closeJumpSession(s.DB, sid, "会话结束")
}

// drainEscSeq 吞掉 ESC 序列的剩余字节（CSI: '[' 后跟 0x40-0x7E 终止符；最多吞 8 字节防异常流）
func drainEscSeq(ks *keyStream) {
	for i := 0; i < 8; i++ {
		select {
		case b, ok := <-ks.ch:
			if !ok {
				return
			}
			// CSI 终止字节：0x40–0x7E（A/B/C/D/H/F 等）
			if i >= 1 && b >= 0x40 && b <= 0x7e {
				return
			}
		case <-time.After(200 * time.Millisecond):
			return // 序列断流（孤立 ESC），交回菜单循环
		}
	}
}

// readLine 从键流读一行：可打印字符回显、退格回显 "\b \b"（擦掉终端上的字符），
// 回车结束。io.Writer 由调用方注入（ch）。
func readLine(ks *keyStream, echo io.Writer) (string, error) {
	var line []byte
	for {
		b, ok := <-ks.ch
		if !ok {
			return "", fmt.Errorf("连接断开")
		}
		switch b {
		case '\r', '\n':
			return string(line), nil
		case 0x7f, 0x08: // 退格：终端擦除（光标左移一格 + 空格覆盖 + 再左移）
			if len(line) > 0 {
				line = line[:len(line)-1]
				_, _ = io.WriteString(echo, "\b \b")
			}
		case 0x03: // Ctrl-C：中止搜索，返回空行
			return "", nil
		default:
			if b >= 0x20 && b < 0x7f { // 只收可打印字符
				line = append(line, b)
				_, _ = echo.Write([]byte{b})
			}
		}
	}
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
