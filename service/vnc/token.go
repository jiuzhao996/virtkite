package vnc

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// TokenEntry 一条 VNC 访问令牌记录。
type TokenEntry struct {
	Token     string
	Host      string
	Port      int
	ExpireAt  time.Time
	CreatedAt time.Time
}

// TTLResolver 返回 token 有效期。main 启动时接到系统设置（service/setting），
// 未接线时退回默认 5 分钟——有效期每次生成 token 时实时读取，改配置无需重启。
var TTLResolver = func() time.Duration { return 5 * time.Minute }

// TokenStore 内存令牌存储，管理 VM VNC 访问的一次性 token。
type TokenStore struct {
	mu     sync.Mutex
	byID   map[uint]string       // vmID -> token
	tokens map[string]TokenEntry // token -> entry
}

// NewTokenStore 创建令牌存储。
func NewTokenStore() *TokenStore {
	return &TokenStore{
		byID:   make(map[uint]string),
		tokens: make(map[string]TokenEntry),
	}
}

// Generate 为指定 VM 生成访问令牌并记录 host:port。
// 每次调用生成新 token，旧 token 失效（保证一次性）。
func (s *TokenStore) Generate(vmID uint, host string, port int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 生成随机 token
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)

	// 移除该 VM 旧 token
	if old, ok := s.byID[vmID]; ok {
		delete(s.tokens, old)
	}
	s.byID[vmID] = token
	s.tokens[token] = TokenEntry{
		Token:     token,
		Host:      host,
		Port:      port,
		ExpireAt:  time.Now().Add(TTLResolver()),
		CreatedAt: time.Now(),
	}
	return token
}

// Lookup 根据 token 返回 host:port。已过期或不存在返回 ok=false。
func (s *TokenStore) Lookup(token string) (string, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.tokens[token]
	if !ok {
		return "", 0, false
	}
	if time.Now().After(entry.ExpireAt) {
		delete(s.tokens, token)
		return "", 0, false
	}
	return entry.Host, entry.Port, true
}
