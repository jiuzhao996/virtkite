package model

import "time"

// VMCredential 虚拟机 SSH 凭据托管（v3 批次 I）。
// 一台 VM 至多一条凭据（vm_id 唯一索引，重复保存 = 覆盖更新），
// 供文件管理 / 应用商店的 use_saved 通道后端内部取用，免去每次重输口令。
//
// ⚠️ 安全红线：凭据永不回传明文——PasswordEnc 存 AES-256-GCM 密文（base64，nonce 前置），
// 解密只发生在服务端（handler.VMCredentialHandler.ResolveFor / Get），前端只能拿到掩码；
// PasswordEnc 与 Salt 的 json tag 为 "-"，即使哪天把模型直接序列化进响应也不会泄露密文与盐。
type VMCredential struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// 所属虚拟机（一台 VM 只存一份凭据；VM 软删后本行成为无害孤儿，不可达）
	VMID uint `gorm:"not null;uniqueIndex" json:"vm_id"`
	// SSH 登录用户名（如 root / ubuntu）
	User string `gorm:"size:50;not null" json:"user"`
	// SSH 端口，省略按 22
	Port int `gorm:"default:22" json:"port"`
	// AES-256-GCM 密文（base64）——密钥由主密钥 + Salt 派生，主密钥不落库
	PasswordEnc string `gorm:"size:500;not null" json:"-"`
	// 密钥派生盐（32 字节随机数的 hex，64 字符），每次保存重新生成；非保密列但随密文成对存取
	Salt      string    `gorm:"size:64;not null" json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
