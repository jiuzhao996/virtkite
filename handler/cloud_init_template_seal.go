package handler

// cloud-init 模板的口令加固。
//
// 背景（2026-10-21 审计）：模板 Spec 直接 json.Marshal 落库，password 是明文；
// List/Get 又原样回传，任何能访问模板接口的 operator 都能读到 admin 保存的口令。
// 违反 AGENTS.md「明文口令不得落库」。
//
// 这里沿用任务 payload 的同一套方案：落库前把 password 换成 password_enc + salt
// （AES-256-GCM，主密钥来自 CREDENTIAL_MASTER_KEY），读取时解密回填，
// 前端拿到的仍是完整 spec（模板编辑/套用需要），但 DB 里不再有明文。

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/virt"
)

// storedTemplateSpec 模板 Spec 的落库形态：其余字段与 virt.CloudInitSpec 一致，
// 口令只以密文形式存在（明文 password 字段在 seal 时被清空）。
type storedTemplateSpec struct {
	virt.CloudInitSpec
	PasswordEnc string `json:"password_enc,omitempty"`
	Salt        string `json:"salt,omitempty"`
}

// sealTemplateSpec 把模板 spec 序列化为落库字符串；含口令时加密。
// 主密钥未配置且有口令 → fail-closed，不允许把明文写进 DB。
func sealTemplateSpec(spec virt.CloudInitSpec, master string) (string, error) {
	stored := storedTemplateSpec{CloudInitSpec: spec}
	if spec.Password != "" {
		if master == "" {
			return "", errors.New("凭据主密钥未配置，无法保存含口令的模板")
		}
		enc, salt, err := secretbox.SealWithMaster(master, spec.Password)
		if err != nil {
			return "", fmt.Errorf("加密模板口令失败: %w", err)
		}
		stored.Password = ""
		stored.PasswordEnc = enc
		stored.Salt = salt
	}
	b, err := json.Marshal(stored)
	if err != nil {
		return "", fmt.Errorf("模板配置序列化失败: %w", err)
	}
	return string(b), nil
}

// openTemplateSpec 解析落库字符串并解密口令。
// 解密失败时仍返回已解析的非口令字段（密码为空），由调用方决定是降级展示还是报错。
func openTemplateSpec(raw, master string) (virt.CloudInitSpec, error) {
	var stored storedTemplateSpec
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return virt.CloudInitSpec{}, err
	}
	spec := stored.CloudInitSpec
	if stored.PasswordEnc == "" {
		return spec, nil // 无口令；或存量明文（password 已随嵌入字段解出）
	}
	if master == "" {
		return spec, errors.New("凭据主密钥未配置，无法解密模板口令")
	}
	pw, err := secretbox.OpenWithMaster(master, stored.Salt, stored.PasswordEnc)
	if err != nil {
		return spec, fmt.Errorf("模板口令解密失败: %w", err)
	}
	spec.Password = string(pw)
	return spec, nil
}

// migratePlaintextTemplate 把存量明文口令就地加密（读取路径顺带迁移，best-effort）。
// 只在确实检测到明文 password 且无密文时触发；写失败不阻塞读取，由 dbx 留痕。
func (h *CloudInitTemplateHandler) migratePlaintextTemplate(row model.CloudInitTemplate) {
	var stored storedTemplateSpec
	if err := json.Unmarshal([]byte(row.Spec), &stored); err != nil {
		return
	}
	if stored.Password == "" || stored.PasswordEnc != "" {
		return
	}
	master := CredentialMasterSecret()
	if master == "" {
		log.Printf("[cloud-init-templates] 模板 %d 含明文口令但主密钥未配置，暂不迁移", row.ID)
		return
	}
	sealed, err := sealTemplateSpec(stored.CloudInitSpec, master)
	if err != nil {
		log.Printf("[cloud-init-templates] 模板 %d 明文口令迁移失败: %v", row.ID, err)
		return
	}
	dbx.PersistBestEffort(h.DB, "cloud-init-template-migrate", func() error {
		return h.DB.Model(&model.CloudInitTemplate{}).Where("id = ?", row.ID).Update("spec", sealed).Error
	})
}
