package handler

// cloud-init 口令边界加密回归测试（2026-10-17 安全审计）。
// 锁定：提交任务前 payload 里不能再出现明文 password，只允许 password_enc/salt。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jiuzhao/vmops/config"
)

func TestSealPayloadCloudInit(t *testing.T) {
	// 加密主密钥在真实启动时由配置注入；单测里显式给一个，避免空密钥
	old := config.GlobalConfig
	config.GlobalConfig = &config.Config{CredentialMasterKey: "test-master-key-32-bytes-long!!"}
	t.Cleanup(func() { config.GlobalConfig = old })

	const plain = "p@ss-w0rd-明文"
	payload := map[string]interface{}{
		"name": "e2e-seal",
		"cloud_init": map[string]interface{}{
			"hostname": "web-01",
			"user":     "ubuntu",
			"password": plain,
		},
		"disks": []interface{}{
			map[string]interface{}{
				"create_gb": float64(10),
				"cloud_init": map[string]interface{}{
					"password": plain + "-disk",
				},
			},
		},
	}

	if err := sealPayloadCloudInit(payload); err != nil {
		t.Fatalf("加密失败: %v", err)
	}

	ci := payload["cloud_init"].(map[string]interface{})
	if _, ok := ci["password"]; ok {
		t.Error("顶层 cloud_init 仍含明文 password")
	}
	if ci["password_enc"] == "" || ci["salt"] == "" {
		t.Error("顶层 cloud_init 缺少 password_enc/salt")
	}
	diskCI := payload["disks"].([]interface{})[0].(map[string]interface{})["cloud_init"].(map[string]interface{})
	if _, ok := diskCI["password"]; ok {
		t.Error("disks[].cloud_init 仍含明文 password")
	}

	// 最终会落库的是 JSON 序列化后的 payload：整串里不能出现明文
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), plain) {
		t.Errorf("序列化后的 payload 仍含明文口令: %s", string(raw))
	}

	// 没有口令时幂等，不应报错或凭空造字段
	p2 := map[string]interface{}{"cloud_init": map[string]interface{}{"user": "u"}}
	if err := sealPayloadCloudInit(p2); err != nil {
		t.Fatalf("无口令时不应报错: %v", err)
	}
}
