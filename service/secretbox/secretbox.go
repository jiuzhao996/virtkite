// Package secretbox 对称加解密小工具（v3 批次 I：SSH 凭据托管）。
//
// 职责单一：主密钥 + 每条记录随机盐派生 AES-256 密钥 → AES-256-GCM 加解密
// （随机 nonce 前置拼进密文，整体 base64 存储）。全部基于标准库
// （crypto/aes + crypto/cipher + crypto/sha256 + crypto/rand），零第三方依赖。
//
// 威胁模型与边界：防的是「数据库泄露后凭据直接可读」——密钥不落库（主密钥来自
// 运行时注入，见 handler.NewVMCredentialHandler），盐每条记录随机（同明文两次加密
// 密文不同，且无法用预计算表攻击）。不防「应用进程与数据库同时失守」（主密钥在
// 进程内存里）——那属主机沦陷，任何应用层加密都无解，不应宣称更强的保证。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// DeriveKey 从主密钥与盐派生 AES-256 密钥：SHA-256(masterSecret || salt)，恒输出 32 字节。
// salt 必须每次保存凭据时随机生成（见 SealWithMaster），随密文一同存库（非保密列，
// 但不同记录不得复用同一盐——否则同明文密文相同，泄露「两台机器口令一致」这一事实）。
func DeriveKey(masterSecret string, salt []byte) []byte {
	h := sha256.New()
	h.Write([]byte(masterSecret)) // hash.Write 固定实现恒成功，无错误可查
	h.Write(salt)
	return h.Sum(nil) // sha256 输出 32 字节 = AES-256 密钥长度
}

// SealWithMaster 一步加密：生成随机盐 → 派生密钥 → AES-256-GCM 加密。
// 返回 (base64 密文, hex 盐)，两者成对落库（对应 model.VMCredential 的
// PasswordEnc / Salt 列）。每次调用生成新盐——覆盖保存同一口令密文也会整体换血。
func SealWithMaster(masterSecret, plaintext string) (cipherB64 string, saltHex string, err error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		// 忽略加密环节的随机数失败等于 fail-open，必须上抛终止保存
		return "", "", fmt.Errorf("生成随机盐失败: %w", err)
	}
	ct, err := Encrypt(DeriveKey(masterSecret, salt), []byte(plaintext))
	if err != nil {
		return "", "", err
	}
	return ct, hex.EncodeToString(salt), nil
}

// OpenWithMaster 一步解密：hex 盐 → 派生密钥 → AES-GCM 解密并校验认证标签。
// 盐/密文损坏、主密钥不匹配（如已轮换）一律返回错误——调用方必须 fail-closed，
// 绝不允许把解密失败降级成空口令继续使用。
func OpenWithMaster(masterSecret, saltHex, cipherB64 string) ([]byte, error) {
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return nil, fmt.Errorf("盐不是合法的 hex: %w", err)
	}
	return Decrypt(DeriveKey(masterSecret, salt), cipherB64)
}

// Encrypt AES-256-GCM 加密：随机 nonce 前置拼接密文体与认证标签，整体 base64（标准编码）输出。
// GCM 自带认证：密文被篡改或密钥不匹配时 Decrypt 直接报错，不存在「解出错误明文」的静默路径。
func Encrypt(key, plaintext []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成随机 nonce 失败: %w", err)
	}
	// Seal(dst=nonce)：把 nonce 拼在密文头部，解密方按 NonceSize 切分
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, plaintext, nil)), nil
}

// Decrypt 解密 Encrypt 的输出（base64 → 切前置 nonce → GCM Open 校验）。
// base64 非法、长度不足（连 nonce+认证标签都放不下）、标签校验不过均返回错误。
func Decrypt(key []byte, b64 string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("密文不是合法的 base64: %w", err)
	}
	n := gcm.NonceSize()
	if len(raw) < n+gcm.Overhead() {
		return nil, fmt.Errorf("密文长度不足（%d 字节，容纳不下 nonce 与认证标签）", len(raw))
	}
	plaintext, err := gcm.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		// 走到这里 = 密文被篡改或派生密钥不匹配（如主密钥已轮换），认证标签拦下了错误明文
		return nil, fmt.Errorf("密文校验失败（密钥不匹配或数据被篡改）: %w", err)
	}
	return plaintext, nil
}

// newGCM 组装 AES-256-GCM 实例（key 须为 16/24/32 字节；本包 DeriveKey 恒输出 32 字节）。
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM 失败: %w", err)
	}
	return gcm, nil
}
