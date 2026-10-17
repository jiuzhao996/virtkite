// tasklog_redact.go：任务日志的密钥脱敏——收集本任务相关明文并在落库/推送前替换。
package tasks

import (
	"encoding/json"
	"strings"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/secretbox"
	"gorm.io/gorm"
)

const (
	// taskLogRedacted 脱敏后的替换文本（与 Coolify 的 logRedactionValues 同义）。
	taskLogRedacted = "REDACTED"
	// taskLogMinSecretLen 参与脱敏的明文最小长度：太短的串（如 "a"、"1"）做全量替换会把
	// 正常日志也打成 REDACTED，得不偿失。
	taskLogMinSecretLen = 4
)

// logRedactor 任务输出脱敏器（对应 Coolify 的 logRedactionValues）。
//
// 性能要点：明文集合在日志器创建时「一次性」收集（含 vm_credentials 解密），
// 之后每行只做若干次 strings.ReplaceAll——绝不「每行输出都把全部凭据解密一遍」。
type logRedactor struct {
	secrets []string
}

// newLogRedactor 收集本任务相关的已知密钥明文。收集失败一律降级为「不脱敏」而非报错：
// 日志脱敏是纵深防御，不能因为收集失败就阻断任务执行。
func newLogRedactor(db *gorm.DB, taskID uint, masterSecret string) *logRedactor {
	r := &logRedactor{}
	if db == nil {
		return r
	}
	var t model.Task
	if err := db.Select("payload", "vm_id").First(&t, taskID).Error; err != nil {
		return r
	}
	payload := map[string]interface{}{}
	if len(t.Payload) > 0 {
		_ = json.Unmarshal([]byte(t.Payload), &payload)
	}
	// 1) cloud-init 密码：payload 里本就是明文，直接收集（无需解密）
	for _, pw := range collectCloudInitPasswords(payload) {
		r.add(pw)
	}
	// 2) VM 托管凭据口令：只取「本任务相关 VM」的凭据解密，不遍历全表
	if masterSecret != "" {
		for _, cred := range collectRelatedCredentials(db, payload, t.VMID) {
			if plain, err := secretbox.OpenWithMaster(masterSecret, cred.Salt, cred.PasswordEnc); err == nil {
				r.add(string(plain))
			}
		}
	}
	return r
}

// add 登记一条待脱敏明文（去重、过滤过短串）。
func (r *logRedactor) add(s string) {
	s = strings.TrimSpace(s)
	if len(s) < taskLogMinSecretLen {
		return
	}
	for _, e := range r.secrets {
		if e == s {
			return
		}
	}
	r.secrets = append(r.secrets, s)
}

// redact 把命中明文的片段替换为 REDACTED。
func (r *logRedactor) redact(s string) string {
	if len(r.secrets) == 0 {
		return s
	}
	for _, sec := range r.secrets {
		if strings.Contains(s, sec) {
			s = strings.ReplaceAll(s, sec, taskLogRedacted)
		}
	}
	return s
}

// collectCloudInitPasswords 从 payload 里收集所有 cloud-init 密码明文：
// 顶层 cloud_init.password、多盘 disks[].cloud_init.password，以及历史遗留的顶层 password。
func collectCloudInitPasswords(payload map[string]interface{}) []string {
	var out []string
	if payload == nil {
		return out
	}
	if ci, ok := payload["cloud_init"].(map[string]interface{}); ok {
		if pw, ok := ci["password"].(string); ok {
			out = append(out, pw)
		}
	}
	if disks, ok := payload["disks"].([]interface{}); ok {
		for _, d := range disks {
			dm, ok := d.(map[string]interface{})
			if !ok {
				continue
			}
			if ci, ok := dm["cloud_init"].(map[string]interface{}); ok {
				if pw, ok := ci["password"].(string); ok {
					out = append(out, pw)
				}
			}
		}
	}
	// 顶层 password 是 app_install 旧前端的明文通道（现已拒收），仍兜底收集以防历史 payload
	if pw, ok := payload["password"].(string); ok {
		out = append(out, pw)
	}
	return out
}

// collectRelatedCredentials 取本任务相关 VM 的托管凭据（只读这些 VM 的，不扫全表）。
// 相关 VM = payload 的 vm_id / targets[] + 任务已关联的 VM。
func collectRelatedCredentials(db *gorm.DB, payload map[string]interface{}, taskVMID *uint) []model.VMCredential {
	ids := map[uint]bool{}
	if taskVMID != nil && *taskVMID > 0 {
		ids[*taskVMID] = true
	}
	if v, ok := payload["vm_id"]; ok {
		if n, ok := toUint(v); ok {
			ids[n] = true
		}
	}
	if arr, ok := payload["targets"].([]interface{}); ok {
		for _, item := range arr {
			if n, ok := toUint(item); ok {
				ids[n] = true
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	list := make([]uint, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	var creds []model.VMCredential
	if err := db.Where("vm_id IN ?", list).Find(&creds).Error; err != nil {
		return nil
	}
	return creds
}

// toUint 把 JSON 反序列化出的数值（float64）或整数转成 uint（失败返回 false）。
func toUint(v interface{}) (uint, bool) {
	switch n := v.(type) {
	case float64:
		if n > 0 && n == float64(uint(n)) {
			return uint(n), true
		}
	case int:
		if n > 0 {
			return uint(n), true
		}
	case uint:
		if n > 0 {
			return n, true
		}
	}
	return 0, false
}
