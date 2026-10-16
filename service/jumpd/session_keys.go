// 本文件负责跳板会话的键流处理：channel 的单读者键流包装（keyStream）、桥接期的按键
// 转发与黑名单拦截（forwardKeys）、ESC 序列吞除与菜单搜索行读取。菜单 ⇄ 桥接主循环见
// session.go，连接目标机后的双向 IO 桥见 session_bridge.go。
package jumpd

import (
	"fmt"
	"io"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

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
