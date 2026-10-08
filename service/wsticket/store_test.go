package wsticket

import (
	"sync"
	"testing"
	"time"
)

// TestIssueConsume 覆盖签发与消费的正向路径。
func TestIssueConsume(t *testing.T) {
	s := NewStore()
	tk, err := s.Issue(7, VMResource("12"))
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if tk == "" {
		t.Fatal("签发的票据为空")
	}
	e, ok := s.Consume(tk)
	if !ok {
		t.Fatal("有效票据应消费成功")
	}
	if e.UserID != 7 || e.Resource != "vm:12" {
		t.Fatalf("票据内容不符：UserID=%d Resource=%q", e.UserID, e.Resource)
	}
}

// TestConsumeIsSingleUse 一次性：同一票据第二次消费必须失败。
// 这是「票据进日志可接受」的前提——泄漏者拿到的是已被消费的废票。
func TestConsumeIsSingleUse(t *testing.T) {
	s := NewStore()
	tk, _ := s.Issue(1, DockerResource("web"))
	if _, ok := s.Consume(tk); !ok {
		t.Fatal("首次消费应成功")
	}
	if _, ok := s.Consume(tk); ok {
		t.Fatal("同一票据第二次消费必须失败（一次性）")
	}
}

// TestConsumeUnknownToken 未签发的票据必须失败。
func TestConsumeUnknownToken(t *testing.T) {
	s := NewStore()
	if _, ok := s.Consume("deadbeef"); ok {
		t.Fatal("未签发的票据不应通过")
	}
}

// TestConsumeExpired 过期票据必须失败（把 TTL 压到毫秒级验证）。
func TestConsumeExpired(t *testing.T) {
	orig := TTL
	TTL = 10 * time.Millisecond
	defer func() { TTL = orig }()

	s := NewStore()
	tk, _ := s.Issue(1, VMResource("1"))
	time.Sleep(30 * time.Millisecond)
	if _, ok := s.Consume(tk); ok {
		t.Fatal("过期票据不应通过")
	}
}

// TestIssuePrunesExpired 签发时顺带清理过期票据，限制 map 增长。
func TestIssuePrunesExpired(t *testing.T) {
	orig := TTL
	TTL = 5 * time.Millisecond
	defer func() { TTL = orig }()

	s := NewStore()
	_, _ = s.Issue(1, VMResource("1"))
	time.Sleep(15 * time.Millisecond)
	_, _ = s.Issue(2, VMResource("2"))

	s.mu.Lock()
	n := len(s.tokens)
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("过期票据应被清理，期望剩 1 张，实际 %d 张", n)
	}
}

// TestStoreConcurrent 并发签发/消费（配合 go test -race）。
func TestStoreConcurrent(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(uid uint) {
			defer wg.Done()
			tk, err := s.Issue(uid, VMResource("x"))
			if err != nil {
				t.Errorf("并发签发失败: %v", err)
				return
			}
			if _, ok := s.Consume(tk); !ok {
				t.Errorf("并发消费应成功")
			}
		}(uint(i + 1))
	}
	wg.Wait()
}
