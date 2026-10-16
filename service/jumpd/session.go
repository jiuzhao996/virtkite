// 本文件负责单个 session channel 的「菜单 ⇄ 桥接」主循环（runSession）与菜单视图过滤
// （viewOf），以及 SSH 协议载荷解析（pty-req / window-change / exit-status）。键流读取与
// 按键转发见 session_keys.go，连接目标机后的双向 IO 桥见 session_bridge.go。
package jumpd

import (
	"encoding/binary"
	"io"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jiuzhao/vmops/model"
)

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
