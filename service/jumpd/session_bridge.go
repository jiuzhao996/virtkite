// 本文件负责连接目标机后的双向 IO 桥：重验授权 → 解密凭据 → vmssh 拨号 → PTY 回放 →
// 双向拷贝（VM→用户 safeCopy、用户→VM forwardKeys）、心跳与目标侧探活、强断登记、
// 全量命令审计。菜单 ⇄ 桥接主循环见 session.go，键流处理见 session_keys.go。
package jumpd

import (
	"errors"
	"io"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
	"github.com/jiuzhao/vmops/service/vmssh"
)

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
