package jumpd

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

// I-1 回归：pty-req 载荷畸形（term 长度声明后不足 8 字节放 w+h）时不得 panic
// （终审 I-1：原守卫 +9 只护到 w，h 的越界读会 panic）
func TestParsePTYReqShortPayload(t *testing.T) {
	cases := [][]byte{
		{},                       // 空载荷
		{0, 0, 0, 0},             // tl=0 但无 w/h
		{0, 0, 0, 0, 0, 0, 0, 0}, // tl=0 但只有 4 字节余量（不够 w+h）
		{0, 0, 0, 9, 'x'},        // tl=9 声明超界
	}
	for i, payload := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("case %d: parsePTYReq 不应 panic: %v", i, r)
				}
			}()
			term, w, h := parsePTYReq(payload)
			_ = term
			_ = w
			_ = h
		}()
	}
	// 合法载荷照常解析
	term, w, h := parsePTYReq([]byte{0, 0, 0, 5, 'x', 't', 'e', 'r', 'm', 0, 0, 0, 120, 0, 0, 0, 40})
	if term != "xterm" || w != 120 || h != 40 {
		t.Fatalf("合法载荷解析不对: %q %d %d", term, w, h)
	}
}

// lockedBuffer 并发安全写器（测试里主 goroutine 轮询 Len 与转发 goroutine Write 并发）
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedBuffer) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Len()
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// fakeChannel 最小 ssh.Channel 假件：预置字节队列 + push（仅测 keyStream 读路径所需）
type fakeChannel struct {
	mu      sync.Mutex
	pending []byte
	readErr error
}

func (f *fakeChannel) Read(p []byte) (int, error) {
	f.mu.Lock()
	pending := f.pending
	f.pending = nil
	f.mu.Unlock()
	if len(pending) == 0 {
		time.Sleep(2 * time.Millisecond) // 模拟无数据时让出调度，避免测试空转打满 CPU
		return 0, nil
	}
	n := copy(p, pending)
	// 一次读不完的余量放回（真实 channel 语义是流式，不丢字节）
	f.mu.Lock()
	f.pending = append(pending[n:], f.pending...)
	f.mu.Unlock()
	return n, f.readErr
}
func (f *fakeChannel) push(b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = append(f.pending, b...)
}
func (f *fakeChannel) Write(p []byte) (int, error)         { return len(p), nil }
func (f *fakeChannel) Close() error                        { return nil }
func (f *fakeChannel) CloseWrite() error                   { return nil }
func (f *fakeChannel) SendRequest(string, bool, []byte) (bool, error) { return false, nil }
func (f *fakeChannel) Stderr() io.ReadWriter                          { return nil }

// I-2 回归：桥接结束后转发停止，剩余按键归菜单（单读模型）
func TestKeyStreamForwardStopsOnQuit(t *testing.T) {
	fake := &fakeChannel{pending: []byte("abc")}
	ks := startKeyReader(fake)

	stdin := &lockedBuffer{}
	quit := make(chan struct{})
	forwardDone := make(chan struct{})
	go func() {
		forwardKeys(ks, stdin, quit)
		close(forwardDone)
	}()

	// 等 abc 被转发（fake 有 2ms 轮询间隔，给足时间）
	deadline := time.Now().Add(2 * time.Second)
	for stdin.Len() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if stdin.Len() != 3 {
		t.Fatalf("abc 应被转发到 stdin，得到 %q", stdin.String())
	}

	// 触发桥接结束 → 转发必须停止
	close(quit)
	select {
	case <-forwardDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("quit 后转发循环未退出")
	}

	// 停止后写入的新字节不得再进 stdin（归菜单）
	fake.push([]byte("xyz"))
	time.Sleep(100 * time.Millisecond)
	if stdin.Len() != 3 {
		t.Fatalf("quit 后转发应停止：stdin=%q", stdin.String())
	}
	// 剩余字节仍可被菜单读取
	for _, want := range []byte("xyz") {
		select {
		case b, ok := <-ks.ch:
			if !ok || b != want {
				t.Fatalf("菜单应读到剩余字节 %q，得到 %v", want, b)
			}
		case <-time.After(time.Second):
			t.Fatalf("菜单读剩余字节超时")
		}
	}
}
