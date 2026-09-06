package handler

import (
	"io"
	"log"
	"runtime/debug"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/console"
)

// ConnectSerial 处理串口控制台（等价 virsh console，免 IP）：
// 通过 libvirt 流式 API（DomainOpenConsoleBidirectional）连接 VM 串口，
// 与浏览器 WebSocket 双向桥接。前端 xterm.js 直接读写 guest 串口 ttyS0。
func (h *VMHandler) ConnectSerial(c *gin.Context) {
	rawConn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	// guest 输出转发、错误帧、管理员强制断开会并发写同一连接，
	// 必须经 console.Conn 串行化，否则 gorilla/websocket 直接 panic 并带走进程。
	conn := console.NewConn(rawConn)
	defer conn.Close()

	// WS 已升级，无法再写 JSON 响应，ID 非法只能走 WS 错误帧；
	// 解析一次复用，既做主键校验（避免未解析字符串直传 GORM）又供会话登记使用。
	vmID, ok := parseID(c.Param("id"))
	if !ok {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "虚拟机 ID 非法"})
		return
	}
	name, err := h.vmNameByID(vmID)
	if err != nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "虚拟机不存在"})
		return
	}

	// guest 输出：libvirt 写入 inW，我们从 inR 读到后转发给浏览器
	inR, inW := io.Pipe()
	// 浏览器输入：我们从 WS 收到后写入 outW，libvirt 从 outR 读取
	outR, outW := io.Pipe()

	errCh := make(chan error, 1)
	go func() {
		// 后台 goroutine 的 panic 无法被 gin Recovery 拦截，会直接终止进程，必须自兜底
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[serial] 打开串口 panic vm=%s panic=%v\n%s", name, rec, debug.Stack())
				// 让主流程从 errCh 得到结果而不是永久等待
				errCh <- console.ErrConnClosed
			}
		}()
		errCh <- h.Virt.OpenConsole(name, "", outR, inW)
	}()
	_ = conn.WriteJSON(gin.H{"type": "connected", "dev": name})

	// 记录串口会话并持有 WS 连接（退出时关闭；管理员可强制断开）
	if h.Sessions != nil {
		uid, uname := taskUserFromContext(c)
		if s := h.Sessions.Open(vmID, name, "serial", uname, uid, c.ClientIP(), conn); s != nil {
			defer h.Sessions.Close(s.ID)
		}
	}
	select {
	case err := <-errCh:
		if err != nil {
			_ = conn.WriteJSON(gin.H{"type": "error", "msg": err.Error()})
		}
		return
	default:
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// 同上：后台 goroutine 必须自兜底 panic
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[serial] 输出转发 panic vm=%s panic=%v\n%s", name, rec, debug.Stack())
			}
		}()
		buf := make([]byte, 4096)
		for {
			n, rerr := inR.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					_ = inW.Close()
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// 浏览器 → guest
	for {
		_, data, rerr := conn.ReadMessage()
		if rerr != nil {
			break
		}
		if _, werr := outW.Write(data); werr != nil {
			break
		}
	}

	_ = outW.Close()
	_ = inW.Close()
	_ = conn.Close()
	wg.Wait()
}

// vmNameByID 根据 DB id 返回 VM 名称（id 由 parseID 解析后传入，走参数化查询）。
func (h *VMHandler) vmNameByID(id uint) (string, error) {
	var vm model.VM
	if err := h.DB.Select("name").First(&vm, id).Error; err != nil {
		return "", err
	}
	return vm.Name, nil
}
