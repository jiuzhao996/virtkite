package vmssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jiuzhao/vmops/model"
	"golang.org/x/crypto/ssh"
)

// memKeyStore HostKeyStore 的内存实现（单测替换 gorm，不依赖 DB；与包内「短连接、
// 无会话状态」的风格一致，互斥锁只为满足 -race 下并发调用回调的防御性要求）。
type memKeyStore struct {
	mu    sync.Mutex
	byKey map[string]*model.HostKey // key = host:port
}

func newMemKeyStore() *memKeyStore {
	return &memKeyStore{byKey: make(map[string]*model.HostKey)}
}

func (m *memKeyStore) Lookup(host string, port int) (*model.HostKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.byKey[host+":"+itoa(port)]; ok {
		cp := *rec
		return &cp, nil
	}
	return nil, nil
}

func (m *memKeyStore) Save(k *model.HostKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byKey[k.Host+":"+itoa(k.Port)] = k
	return nil
}

// failLookup 查询恒失败的 store（验证 fail-closed 路径）。
type failLookup struct{}

func (failLookup) Lookup(string, int) (*model.HostKey, error) { return nil, errors.New("db down") }
func (failLookup) Save(*model.HostKey) error                  { return nil }

// itoa 测试内的小工具（strconv.Itoa 亦可，独立实现避免与被测代码共享转换逻辑）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// genEd25519 生成一对 ed25519 密钥对应的 ssh.PublicKey（测试辅助）。
func genEd25519(t *testing.T) ssh.PublicKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 ed25519 密钥失败: %v", err)
	}
	pub, err := ssh.NewPublicKey(priv.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatalf("转换 ssh.PublicKey 失败: %v", err)
	}
	return pub
}

// TestFingerprintSHA256 覆盖指纹格式化纯函数。
//
// 期望值用独立实现计算（sha256 摘要 + 标准 base64，不带 padding 的截断由
// ssh.FingerprintSHA256 的既定格式决定），与被测函数不共享代码路径：
// 指纹错一位 = TOFU 把中间人当成合法主机，必须独立验证。
func TestFingerprintSHA256(t *testing.T) {
	pub := genEd25519(t)
	sum := sha256.Sum256(pub.Marshal())
	want := "SHA256:" + base64.StdEncoding.EncodeToString(sum[:])[:43]

	got := FingerprintSHA256(pub)
	if got != want {
		t.Errorf("指纹不符：期望 %q，实际 %q", want, got)
	}
	if !strings.HasPrefix(got, "SHA256:") {
		t.Errorf("指纹缺少 SHA256: 前缀：%q", got)
	}

	t.Run("同一公钥指纹稳定", func(t *testing.T) {
		if again := FingerprintSHA256(pub); again != got {
			t.Errorf("同一公钥两次指纹不同：%q vs %q", got, again)
		}
	})

	t.Run("不同公钥指纹不同", func(t *testing.T) {
		other := FingerprintSHA256(genEd25519(t))
		if other == got {
			t.Error("不同公钥算出了相同指纹（会掩盖中间人替换）")
		}
	})
}

// TestTOFUCallback 覆盖主机密钥回调三条主路径：首次记录 / 一致放行 / 不一致拒绝。
func TestTOFUCallback(t *testing.T) {
	first := genEd25519(t)
	second := genEd25519(t)

	t.Run("首次连接：记录指纹并放行", func(t *testing.T) {
		store := newMemKeyStore()
		cb := tofuCallback(store)
		if err := cb("10.0.0.5:22", nil, first); err != nil {
			t.Fatalf("首连应放行，实际被拒: %v", err)
		}
		rec, err := store.Lookup("10.0.0.5", 22)
		if err != nil || rec == nil {
			t.Fatalf("首连后应落库指纹: rec=%v err=%v", rec, err)
		}
		if rec.Fingerprint != FingerprintSHA256(first) {
			t.Errorf("落库指纹与公钥不符：%q", rec.Fingerprint)
		}
		if rec.KeyType != "ssh-ed25519" {
			t.Errorf("key_type 应为 ssh-ed25519，实际 %q", rec.KeyType)
		}
	})

	t.Run("同机不同端口视为不同记录", func(t *testing.T) {
		store := newMemKeyStore()
		cb := tofuCallback(store)
		if err := cb("10.0.0.5:22", nil, first); err != nil {
			t.Fatalf("22 端口首连应放行: %v", err)
		}
		if err := cb("10.0.0.5:2222", nil, second); err != nil {
			t.Fatalf("2222 端口是另一条记录，应按首连放行: %v", err)
		}
	})

	t.Run("IPv6 地址还原 host 段（[fd00::1]:22）", func(t *testing.T) {
		store := newMemKeyStore()
		cb := tofuCallback(store)
		if err := cb("[fd00::1]:22", nil, first); err != nil {
			t.Fatalf("IPv6 首连应放行: %v", err)
		}
		rec, _ := store.Lookup("fd00::1", 22)
		if rec == nil {
			t.Fatal("IPv6 host 段应去掉方括号后落库")
		}
	})

	t.Run("指纹一致放行", func(t *testing.T) {
		store := newMemKeyStore()
		cb := tofuCallback(store)
		if err := cb("10.0.0.5:22", nil, first); err != nil {
			t.Fatalf("首连失败: %v", err)
		}
		for i := 0; i < 3; i++ {
			if err := cb("10.0.0.5:22", nil, first); err != nil {
				t.Fatalf("指纹一致应放行（第 %d 次）: %v", i+1, err)
			}
		}
	})

	t.Run("指纹不一致拒绝且错误可被 errors.Is 识别", func(t *testing.T) {
		store := newMemKeyStore()
		cb := tofuCallback(store)
		if err := cb("10.0.0.5:22", nil, first); err != nil {
			t.Fatalf("首连失败: %v", err)
		}
		err := cb("10.0.0.5:22", nil, second)
		if err == nil {
			t.Fatal("指纹不一致必须拒绝连接")
		}
		if !errors.Is(err, ErrHostKeyMismatch) {
			t.Fatalf("错误链应可被 errors.Is(ErrHostKeyMismatch) 识别: %v", err)
		}
		if !strings.Contains(err.Error(), "目标主机密钥指纹与首次连接记录不一致") {
			t.Errorf("错误文案缺少固定中文提示: %q", err.Error())
		}
	})

	t.Run("存储未注入：一律拒绝（fail-closed）", func(t *testing.T) {
		cb := tofuCallback(nil)
		if err := cb("10.0.0.5:22", nil, first); err == nil {
			t.Fatal("store 为 nil 时必须拒绝，不能退化成不校验")
		}
	})

	t.Run("存储查询失败：拒绝连接（fail-closed）", func(t *testing.T) {
		cb := tofuCallback(failLookup{})
		if err := cb("10.0.0.5:22", nil, first); err == nil {
			t.Fatal("查询失败时必须拒绝，放行等于回退到不校验")
		}
	})

	t.Run("畸形地址：拒绝连接（防御）", func(t *testing.T) {
		cb := tofuCallback(newMemKeyStore())
		if err := cb("no-port-in-here", nil, first); err == nil {
			t.Fatal("无法解析 host:port 时必须拒绝")
		}
	})
}

// TestSetHostKeyStoreTOFUHostKeyCallback 覆盖全局注入与 TOFUHostKeyCallback 的接线。
func TestSetHostKeyStoreTOFUHostKeyCallback(t *testing.T) {
	prev := currentStore()
	defer SetHostKeyStore(prev) // 还原全局状态，避免污染其他用例

	store := newMemKeyStore()
	SetHostKeyStore(store)
	if currentStore() == nil {
		t.Fatal("注入后 currentStore 不应为 nil")
	}
	pub := genEd25519(t)
	if err := TOFUHostKeyCallback()("192.168.122.50:22", nil, pub); err != nil {
		t.Fatalf("注入全局存储后回调应放行首连: %v", err)
	}
}
