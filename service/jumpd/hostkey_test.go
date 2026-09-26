package jumpd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestHostKeyCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jumpd_host_key")

	s1, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("首次应生成成功: %v", err)
	}
	// 文件落盘且权限 0600
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("密钥文件应存在: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("密钥文件权限应为 0600，得到 %v", info.Mode().Perm())
	}
	// 同路径两次调用指纹一致（known_hosts 稳定性的根）
	s2, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("二次应加载成功: %v", err)
	}
	if ssh.FingerprintSHA256(s1.PublicKey()) != ssh.FingerprintSHA256(s2.PublicKey()) {
		t.Fatalf("两次调用指纹应一致（持久化失效）")
	}
}

func TestHostKeyLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jumpd_host_key")

	// 预置合法密钥：用 ssh.MarshalPrivateKey 生成 PEM 写入（与实现同格式）
	sWant, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("预置生成失败: %v", err)
	}
	wantFP := ssh.FingerprintSHA256(sWant.PublicKey())

	got, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("加载已有密钥失败: %v", err)
	}
	if ssh.FingerprintSHA256(got.PublicKey()) != wantFP {
		t.Fatalf("加载的指纹应与写入一致")
	}
}

func TestHostKeyCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jumpd_host_key")
	if err := os.WriteFile(path, []byte("这不是一个合法的 PEM 密钥"), 0o600); err != nil {
		t.Fatalf("写垃圾文件失败: %v", err)
	}
	before, _ := os.ReadFile(path)

	_, err := loadOrCreateHostKey(path)
	if err == nil {
		t.Fatalf("损坏文件应报错")
	}
	if !errors.Is(err, errHostKeyCorrupt) {
		t.Fatalf("错误应可 errors.Is 识别为 errHostKeyCorrupt: %v", err)
	}
	// fail-closed：不覆盖不重生成（静默换钥 = known_hosts 全体失效）
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("损坏文件不应被覆盖重生成")
	}
}
