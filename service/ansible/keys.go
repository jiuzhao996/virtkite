// keys.go：平台 ansible SSH 密钥对（P4-S4 免密渐进）。
//
// 平台在 data/ansible/id_ed25519 托管一对密钥：新建 VM 经 cloud-init 注入公钥
// （免密直通），存量 VM 用「分发公钥」动作经现有凭据通道补注入；此后 ansible
// 执行对该 VM 走私钥通道，inventory 不再瞬时承载口令文件。明文口令通道保留给
// 未分发过公钥的存量 VM（口径见 ansible.go 包头注释）。
package ansible

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

var (
	keyMu        sync.Mutex
	keyPrivate   string // 私钥路径（inventory ansible_ssh_private_key_file 用）
	keyPublicKey string // 公钥一行（authorized_keys / cloud-init 注入用）
)

// EnsureKeyPair 返回（私钥路径, 公钥, 错误）。进程内缓存；文件缺失则生成
// ed25519 对（私钥 0600，公钥 0644）。重复调用幂等。
func EnsureKeyPair(dir string) (string, string, error) {
	keyMu.Lock()
	defer keyMu.Unlock()
	if keyPrivate != "" && keyPublicKey != "" {
		return keyPrivate, keyPublicKey, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("创建密钥目录失败: %w", err)
	}
	privPath := filepath.Join(dir, "id_ed25519")
	pubPath := privPath + ".pub"

	if _, err := os.Stat(privPath); err == nil {
		pub, rerr := os.ReadFile(pubPath)
		if rerr != nil {
			return "", "", fmt.Errorf("读取公钥失败（私钥在而公钥缺失，请删除私钥重生成）: %w", rerr)
		}
		keyPrivate, keyPublicKey = privPath, strings.TrimSpace(string(pub))
		return keyPrivate, keyPublicKey, nil
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("生成密钥失败: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("构造 SSH 公钥失败: %w", err)
	}
	authorized := ssh.MarshalAuthorizedKey(sshPub) // 一行 "ssh-ed25519 AAA... comment\n"

	block, err := ssh.MarshalPrivateKey(priv, "vmops-ansible")
	if err != nil {
		return "", "", fmt.Errorf("序列化私钥失败: %w", err)
	}
	if werr := os.WriteFile(privPath, pem.EncodeToMemory(block), 0o600); werr != nil {
		return "", "", fmt.Errorf("写私钥失败: %w", werr)
	}
	if werr := os.WriteFile(pubPath, authorized, 0o644); werr != nil {
		return "", "", fmt.Errorf("写公钥失败: %w", werr)
	}
	keyPrivate, keyPublicKey = privPath, strings.TrimSpace(string(authorized))
	log.Printf("[ansible] 平台密钥对已生成: %s", privPath)
	return keyPrivate, keyPublicKey, nil
}

// FingerprintPair 公钥指纹（引擎状态卡展示用，重探测不改缓存）。
func FingerprintPair(pubKey string) string {
	if pubKey == "" {
		return ""
	}
	parts := strings.Fields(pubKey)
	if len(parts) < 2 {
		return pubKey
	}
	// sha256 指纹取前 16 位展示（完整指纹无运维价值，短指纹够辨识）
	sum := sha256.Sum256([]byte(parts[1]))
	return fmt.Sprintf("%s:%x", parts[0], sum[:8])
}

// ForceReloadKeyPair 清缓存重读（测试用；生产无调用方）。
func ForceReloadKeyPair() {
	keyMu.Lock()
	defer keyMu.Unlock()
	keyPrivate, keyPublicKey = "", ""
}
