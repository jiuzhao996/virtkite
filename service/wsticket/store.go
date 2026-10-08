// Package wsticket 提供一次性、短时有效的 WebSocket 连接票据。
//
// 为什么需要：浏览器 WebSocket 无法自定义请求头，SSH 终端 / 串口 / 容器终端 /
// 容器日志流的升级请求只能把凭证塞进 URL query（历史实现是 ?token=<JWT>）。
// 长期 JWT 一旦进 URL，就会落进 gin 访问日志、反向代理日志与 Referer，泄漏的是
// 「有效期小时级、全权限」的凭证。改用本包签发的票据后，同样进 URL，但泄漏的只是
// 一张「30 秒内、且一经使用即作废」的废票。
//
// 与 service/vnc 的 TokenStore 同构（crypto/rand + TTL + 一次性），但语义更严：
// 额外绑定「用户 + 目标资源」，阻断票据跨资源重放（拿 docker 票打 vms 终端）。
package wsticket

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// TTL 票据有效期。签发（POST 取票）到 WS 升级是即时的，30 秒足以吸收页面点击到
// 建连的延迟；不照抄 vnc 的 5 分钟——那是用户在 novnc 页面里的交互场景，暴露窗口更大。
// 声明为变量而非常量：让单测能把有效期压到毫秒级验证过期路径（生产不修改）。
var TTL = 30 * time.Second

// Entry 一张票据记录。
type Entry struct {
	UserID    uint
	Resource  string // 目标资源标识，如 "vm:12" / "docker:web1"
	ExpireAt  time.Time
	CreatedAt time.Time
}

// Store 进程内票据库。随进程重启清空——票据本就是短时凭证，无需持久化。
type Store struct {
	mu     sync.Mutex
	tokens map[string]Entry
}

// NewStore 创建票据库。
func NewStore() *Store {
	return &Store{tokens: make(map[string]Entry)}
}

// Issue 为用户签发一张绑定到 resource 的票据并返回。
// 随机源失败时返回错误且不签发——全零/可预测票据等同放行任意连接，不能带病签发。
func (s *Store) Issue(userID uint, resource string) (string, error) {
	// 持锁前读随机源，避免熵源抖动拉长临界区（同 vnc.TokenStore）
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("读取随机数失败：%w", err)
	}
	token := hex.EncodeToString(b)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	// 顺带清理过期票据，限制 map 增长（不另起后台协程，避免生命周期负担）
	for k, e := range s.tokens {
		if now.After(e.ExpireAt) {
			delete(s.tokens, k)
		}
	}
	s.tokens[token] = Entry{
		UserID:    userID,
		Resource:  resource,
		ExpireAt:  now.Add(TTL),
		CreatedAt: now,
	}
	return token, nil
}

// Consume 校验并消费票据：命中即删（一次性），过期或不存在返回 ok=false。
func (s *Store) Consume(token string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tokens[token]
	if !ok {
		return Entry{}, false
	}
	delete(s.tokens, token)
	if time.Now().After(e.ExpireAt) {
		return Entry{}, false
	}
	return e, true
}

// VMResource VM 终端 / 串口的资源标识（粒度取资源本身，同资源下各通道同属一个 RBAC 组）。
func VMResource(id string) string { return "vm:" + id }

// DockerResource 容器终端 / 日志流的资源标识。
func DockerResource(id string) string { return "docker:" + id }
