package console

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialTestConn 起一个回环 WebSocket 服务端，返回服务端侧的 *Conn 与客户端原始连接。
// 服务端侧用被测的 Conn 包装，客户端侧只负责把帧读干净（避免写缓冲打满而阻塞）。
func dialTestConn(t *testing.T) (server *Conn, client *websocket.Conn) {
	t.Helper()

	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	ready := make(chan *Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("服务端升级 WebSocket 失败: %v", err)
			return
		}
		ready <- NewConn(ws)
		// 保持 handler 存活，直到测试结束关闭连接
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	cli, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("客户端连接失败: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	select {
	case server = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("等待服务端连接就绪超时")
	}
	return server, cli
}

// TestConnConcurrentWrite 验证多 goroutine 并发写不再触发
// gorilla/websocket 的 "concurrent write to websocket connection" panic。
// 这是 SSH 桥（stdout/stderr/主循环）与串口桥的真实并发模型，
// 必须配合 -race 运行：go test -race ./service/console/
func TestConnConcurrentWrite(t *testing.T) {
	server, client := dialTestConn(t)

	// 客户端持续读，防止服务端写阻塞在 TCP 缓冲上
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			if _, _, err := client.ReadMessage(); err != nil {
				return
			}
		}
	}()

	const writers, perWriter = 8, 50
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				var err error
				// 混合二进制帧与 JSON 帧，覆盖两条写路径
				if i%2 == 0 {
					err = server.WriteMessage(websocket.BinaryMessage, []byte("guest output"))
				} else {
					err = server.WriteJSON(map[string]any{"type": "pong", "writer": id})
				}
				if err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("并发写返回错误: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("关闭连接失败: %v", err)
	}
	<-drained
}

// TestConnWriteAfterClose 验证连接关闭后写入返回 ErrConnClosed 而非向已关连接写入。
// 桥接 goroutine 依赖该错误退出转发循环。
func TestConnWriteAfterClose(t *testing.T) {
	server, _ := dialTestConn(t)

	if err := server.Close(); err != nil {
		t.Fatalf("首次关闭应成功，得到: %v", err)
	}

	if err := server.WriteMessage(websocket.BinaryMessage, []byte("x")); !errors.Is(err, ErrConnClosed) {
		t.Errorf("WriteMessage 应返回 ErrConnClosed，得到: %v", err)
	}
	if err := server.WriteJSON(map[string]string{"type": "pong"}); !errors.Is(err, ErrConnClosed) {
		t.Errorf("WriteJSON 应返回 ErrConnClosed，得到: %v", err)
	}
}

// TestConnCloseIdempotent 验证重复关闭安全：
// handler 的 defer Close 与管理员强制断开的 CloseWithReason 会双重触发。
func TestConnCloseIdempotent(t *testing.T) {
	server, _ := dialTestConn(t)

	if err := server.CloseWithReason("管理员已断开连接"); err != nil {
		t.Fatalf("首次关闭应成功，得到: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Errorf("重复关闭应返回 nil（幂等），得到: %v", err)
	}
	if err := server.CloseWithReason("再来一次"); err != nil {
		t.Errorf("重复关闭应返回 nil（幂等），得到: %v", err)
	}
}

// TestConnCloseWithReasonDeliversFrame 验证强制断开时对端能收到带中文原因的关闭帧。
func TestConnCloseWithReasonDeliversFrame(t *testing.T) {
	server, client := dialTestConn(t)

	const reason = "管理员已断开连接"
	if err := server.CloseWithReason(reason); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err := client.ReadMessage()
	if err == nil {
		t.Fatal("客户端应读到关闭错误")
	}
	var ce *websocket.CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("应为 CloseError，得到 %T: %v", err, err)
	}
	if ce.Code != websocket.CloseNormalClosure {
		t.Errorf("关闭码应为 %d，得到 %d", websocket.CloseNormalClosure, ce.Code)
	}
	if ce.Text != reason {
		t.Errorf("关闭原因应为 %q，得到 %q", reason, ce.Text)
	}
}

// TestConnCloseUnblocksReader 验证关闭后阻塞中的 ReadMessage 会返回错误，
// 从而让 handler 的读循环退出（管理员强制断开的收敛依赖此行为）。
func TestConnCloseUnblocksReader(t *testing.T) {
	server, _ := dialTestConn(t)

	done := make(chan error, 1)
	go func() {
		_, _, err := server.ReadMessage()
		done <- err
	}()

	// 给读 goroutine 一点时间真正阻塞在 Read 上
	time.Sleep(50 * time.Millisecond)
	_ = server.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Error("关闭后 ReadMessage 应返回错误")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("关闭后 ReadMessage 未返回，读循环无法退出")
	}
}
