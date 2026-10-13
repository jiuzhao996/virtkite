package vnc

import (
	"encoding/hex"
	"sync"
	"testing"
	"time"
)

// TestGenerateAndLookup 覆盖生成令牌并据令牌换回 host:port 的正向路径。
//
// 风险点：token 是前端 WebSocket 拉 VNC 的唯一凭证，Lookup 必须原样吐回
// 生成时记录的 host/port，端口写错会让用户连到别的服务甚至别的 VM 的 VNC。
func TestGenerateAndLookup(t *testing.T) {
	s := NewTokenStore()
	tok, err := s.Generate(7, "10.0.0.9", 5901)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	host, port, ok := s.Lookup(tok)
	if !ok {
		t.Fatal("刚生成的令牌应可查到")
	}
	if host != "10.0.0.9" || port != 5901 {
		t.Fatalf("Lookup 返回 (%q, %d)，期望 (10.0.0.9, 5901)", host, port)
	}
}

// TestTokenShape 锁死令牌的形态：16 字节随机数的十六进制、小写、32 字符。
//
// 风险点：token 进 URL，长度/字符集决定了离线爆破难度。随机数被缩水（例如
// 只读 8 字节）或改用非加密随机源，形态会静默变化而不报错，此用例守住下限。
func TestTokenShape(t *testing.T) {
	s := NewTokenStore()
	tok, err := s.Generate(1, "h", 5900)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if len(tok) != 32 {
		t.Errorf("token 长度期望 32，实际 %d（%q）", len(tok), tok)
	}
	raw, err := hex.DecodeString(tok)
	if err != nil {
		t.Fatalf("token 不是合法十六进制: %v", err)
	}
	if len(raw) != 16 {
		t.Errorf("token 解码后期望 16 字节，实际 %d", len(raw))
	}
	if tok != hex.EncodeToString(raw) {
		t.Error("token 非小写十六进制编码")
	}
}

// TestGenerateIsOneTimePerVM 同一 VM 再次生成令牌后，旧令牌必须失效。
//
// 风险点：这是「一次性 token」语义的核心——每次打开控制台都签发新票，旧票作废，
// 使泄漏到日志/历史里的旧 token 立即变成废票。若 byID 覆盖逻辑被删，旧 token
// 会一直有效到过期，攻击面随每次打开控制台单调增长。
func TestGenerateIsOneTimePerVM(t *testing.T) {
	s := NewTokenStore()
	old, err := s.Generate(5, "h", 5900)
	if err != nil {
		t.Fatalf("首次 Generate 失败: %v", err)
	}
	newTok, err := s.Generate(5, "h", 5900)
	if err != nil {
		t.Fatalf("再次 Generate 失败: %v", err)
	}
	if old == newTok {
		t.Fatal("同一 VM 两次生成得到了相同 token")
	}
	if _, _, ok := s.Lookup(old); ok {
		t.Error("旧 token 在重新签发后仍然有效（一次性语义被破坏）")
	}
	if _, _, ok := s.Lookup(newTok); !ok {
		t.Error("新 token 应有效")
	}
}

// TestMultipleVMsIndependent 不同 VM 的令牌互不影响。
//
// 风险点：byID 是 vmID -> token 的映射，覆盖只应作用于同一 VM。若误用全局单键，
// 开 B 的控制台会把 A 的令牌顶掉，A 的会话被莫名踢断。
func TestMultipleVMsIndependent(t *testing.T) {
	s := NewTokenStore()
	a, _ := s.Generate(1, "h1", 5900)
	b, _ := s.Generate(2, "h2", 5901)
	if _, port, ok := s.Lookup(a); !ok || port != 5900 {
		t.Fatalf("VM1 令牌失效或端口错: ok=%v port=%d", ok, port)
	}
	if host, port, ok := s.Lookup(b); !ok || host != "h2" || port != 5901 {
		t.Fatalf("VM2 令牌失效或内容错: ok=%v host=%q port=%d", ok, host, port)
	}
}

// TestLookupUnknown 未签发的令牌必须查不到。
//
// 风险点：Lookup 是鉴权入口，任何「查不到也返回 ok」的退化都会放行任意连接。
func TestLookupUnknown(t *testing.T) {
	s := NewTokenStore()
	if _, _, ok := s.Lookup("deadbeef"); ok {
		t.Fatal("未签发的令牌不应通过")
	}
	if _, _, ok := s.Lookup(""); ok {
		t.Fatal("空令牌不应通过")
	}
}

// TestLookupExpired 过期令牌必须失效（把 TTL 压到负值即时过期验证）。
//
// 风险点：TTLResolver 允许运行时改配置（改有效期无需重启）。这里把 TTL 设成负数，
// 让 ExpireAt 落在过去，验证 Lookup 的过期判断与惰性删除分支确实被执行到——
// 缺了这段判断，token 将永久有效。
func TestLookupExpired(t *testing.T) {
	orig := TTLResolver
	TTLResolver = func() time.Duration { return -time.Second }
	defer func() { TTLResolver = orig }()

	s := NewTokenStore()
	tok, err := s.Generate(1, "h", 5900)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if _, _, ok := s.Lookup(tok); ok {
		t.Fatal("已过期令牌不应通过")
	}
	// 过期后再查仍应失败（确认惰性删除后不会残留可用记录）。
	if _, _, ok := s.Lookup(tok); ok {
		t.Fatal("过期并被删除的令牌二次查询仍通过")
	}
}

// TestGenerateUniqueAcrossCalls 大量生成时令牌不得碰撞。
//
// 风险点：token 是 16 字节随机数，正常不会撞。此用例更多是防「常量/时间戳」这类
// 退化实现混进来——它们在高并发签发下会产出重复凭证，等于任意 VM 之间可越权。
func TestGenerateUniqueAcrossCalls(t *testing.T) {
	s := NewTokenStore()
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok, err := s.Generate(uint(i), "h", 5900)
		if err != nil {
			t.Fatalf("第 %d 次 Generate 失败: %v", i, err)
		}
		if seen[tok] {
			t.Fatalf("令牌碰撞: %q", tok)
		}
		seen[tok] = true
	}
}

// TestConcurrentAccess 并发 Generate/Lookup 不得 panic 或数据竞争
// （用 go test -race 才有意义）。
//
// 风险点：TokenStore 持 sync.Mutex 保护 byID/tokens 两张 map。若日后有人为「提速」
// 去掉锁或改成读写分离，并发 map 读写会直接 panic 把进程带走，且 gin Recovery
// 拦不住——本用例是那把锁的回归护栏。
func TestConcurrentAccess(t *testing.T) {
	s := NewTokenStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(vmID int) {
			defer wg.Done()
			tok, err := s.Generate(uint(vmID), "h", 5900)
			if err != nil {
				return
			}
			s.Lookup(tok)
			s.Lookup("nope")
		}(i)
	}
	wg.Wait()
}
