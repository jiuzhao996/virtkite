package handler

import (
	"io"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/jiuzhao/vmops/model"
)

// ConnectSerial 处理串口控制台（等价 virsh console，免 IP）：
// 通过 libvirt 流式 API（DomainOpenConsoleBidirectional）连接 VM 串口，
// 与浏览器 WebSocket 双向桥接。前端 xterm.js 直接读写 guest 串口 ttyS0。
func (h *VMHandler) ConnectSerial(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	name, err := h.vmNameByID(c.Param("id"))
	if err != nil {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "虚拟机不存在"})
		return
	}
	var vmID uint
	if n, perr := strconv.ParseUint(c.Param("id"), 10, 32); perr == nil {
		vmID = uint(n)
	}

	// guest 输出：libvirt 写入 inW，我们从 inR 读到后转发给浏览器
	inR, inW := io.Pipe()
	// 浏览器输入：我们从 WS 收到后写入 outW，libvirt 从 outR 读取
	outR, outW := io.Pipe()

	errCh := make(chan error, 1)
	go func() {
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

// vmNameByID 根据 DB id 返回 VM 名称。
func (h *VMHandler) vmNameByID(id string) (string, error) {
	var vm model.VM
	if err := h.DB.Select("name").First(&vm, id).Error; err != nil {
		return "", err
	}
	return vm.Name, nil
}
