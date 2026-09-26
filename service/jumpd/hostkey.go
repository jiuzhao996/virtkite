package jumpd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"

	"golang.org/x/crypto/ssh"
)

// errHostKeyCorrupt 主机密钥文件损坏的哨兵错误：调用方可 errors.Is 识别后拒绝启动
var errHostKeyCorrupt = errors.New("跳板主机密钥文件损坏")

// loadOrCreateHostKey 加载或首次生成跳板自身的 ed25519 主机密钥。
// 首次生成后持久化（0600），客户端 known_hosts 以此为准——换了钥等于全世界的
// known_hosts 静默失效，所以损坏文件 fail-closed 拒绝启动，绝不覆盖重生成。
// 注意：目录需已存在（data/ 由部署侧创建），不存在时报错而非静默 mkdir，
// 避免在意外挂载点（如只读盘/错误路径）下默默写文件。
func loadOrCreateHostKey(path string) (ssh.Signer, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		signer, parseErr := ssh.ParsePrivateKey(raw)
		if parseErr != nil {
			// fail-closed：宁可拒绝启动也不能让 known_hosts 静默失效
			log.Printf("[jumpd] 主机密钥解析失败，拒绝启动（不覆盖原文件）: %v", parseErr)
			return nil, fmt.Errorf("%w: %v", errHostKeyCorrupt, parseErr)
		}
		return signer, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取跳板主机密钥失败: %w", err)
	}

	// 首次生成（公钥指纹经 signer.PublicKey() 取，避免双份引用）
	_, priv, genErr := ed25519.GenerateKey(rand.Reader)
	if genErr != nil {
		return nil, fmt.Errorf("生成 ed25519 密钥失败: %w", genErr)
	}
	block, marshalErr := ssh.MarshalPrivateKey(priv, "vmops-jumpd")
	if marshalErr != nil {
		return nil, fmt.Errorf("编码私钥失败: %w", marshalErr)
	}
	if writeErr := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); writeErr != nil {
		return nil, fmt.Errorf("写入主机密钥文件失败: %w", writeErr)
	}
	signer, signErr := ssh.NewSignerFromKey(priv)
	if signErr != nil {
		return nil, fmt.Errorf("构造签名器失败: %w", signErr)
	}
	log.Printf("[jumpd] 已生成跳板主机密钥 %s（指纹 %s）", path, ssh.FingerprintSHA256(signer.PublicKey()))
	return signer, nil
}
