package handler

// cloud-init 口令边界加密。
//
// 背景（2026-10-17 安全审计实测）：建机 payload 会原样落 tasks.payload，历史上
// cloud_init.password 是明文——任务详情虽对 API 隐藏 payload，但 DB 里能直接读到
// 明文口令，违反 AGENTS.md 第 15 条「明文口令不得落库」。
//
// 这里在提交任务前把明文替换为 password_enc + salt（AES-256-GCM，主密钥独立）；
// executor 用同一主密钥解密后生成 seed ISO。与 provision 块同源同密钥。

import (
	"errors"

	"github.com/jiuzhao/vmops/service/secretbox"
)

// sealCloudInitMap 就地加密 map 形式的 cloud-init：password → password_enc/salt。
// 没有明文 password 时原样返回（兼容已加密或本就无口令的场景）。
func sealCloudInitMap(m map[string]interface{}) error {
	if m == nil {
		return nil
	}
	pw, _ := m["password"].(string)
	if pw == "" {
		return nil
	}
	master := CredentialMasterSecret()
	if master == "" {
		// 空主密钥等于用可预测密钥加密，必须 fail-closed
		return errors.New("凭据主密钥未配置，无法加密 cloud-init 口令")
	}
	cipherB64, saltHex, err := secretbox.SealWithMaster(master, pw)
	if err != nil {
		return err
	}
	m["password_enc"] = cipherB64
	m["salt"] = saltHex
	delete(m, "password")
	return nil
}

// sealPayloadCloudInit 处理 payload 里所有可能出现 cloud-init 的位置：
// 顶层 cloud_init 与 disks[].cloud_init（磁盘级 cloud-init 为兼容旧客户端保留）。
func sealPayloadCloudInit(payload map[string]interface{}) error {
	if payload == nil {
		return nil
	}
	if ci, ok := payload["cloud_init"].(map[string]interface{}); ok {
		if err := sealCloudInitMap(ci); err != nil {
			return err
		}
	}
	if disks, ok := payload["disks"].([]interface{}); ok {
		for _, d := range disks {
			dm, ok := d.(map[string]interface{})
			if !ok {
				continue
			}
			if ci, ok := dm["cloud_init"].(map[string]interface{}); ok {
				if err := sealCloudInitMap(ci); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
