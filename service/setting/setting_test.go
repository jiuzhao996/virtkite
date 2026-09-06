package setting

import (
	"strings"
	"testing"
)

// TestValidate 白名单与取值范围校验（纯函数，不依赖 DB）。
func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		{"合法池名", KeyDefaultStoragePool, "vmops", false},
		{"池名含点连字符", KeyDefaultStoragePool, "nvme-images.v2", false},
		{"池名为空", KeyDefaultStoragePool, "", true},
		{"池名含路径分隔", KeyDefaultStoragePool, "a/b", true},
		{"池名超长", KeyDefaultStoragePool, strings.Repeat("a", 65), true},
		{"池名含非法字符", KeyDefaultStoragePool, "池", true},
		{"token 有效期合法下界", KeyVNCTokenTTLMin, "1", false},
		{"token 有效期合法上界", KeyVNCTokenTTLMin, "60", false},
		{"token 有效期越上界", KeyVNCTokenTTLMin, "61", true},
		{"token 有效期非数字", KeyVNCTokenTTLMin, "abc", true},
		{"会话过期合法下界", KeyVNCStaleMin, "5", false},
		{"会话过期合法上界", KeyVNCStaleMin, "1440", false},
		{"会话过期越下界", KeyVNCStaleMin, "4", true},
		{"会话过期越上界", KeyVNCStaleMin, "1441", true},
		{"未知键拒绝", "not_a_key", "1", true},
		{"未知键空值拒绝", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.key, tc.value)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate(%q, %q) err=%v, wantErr=%v", tc.key, tc.value, err, tc.wantErr)
			}
		})
	}
}

// TestDefaultResolvers 未接线时 resolver 必须返回与旧硬编码一致的默认值，
// 保证「系统设置未配置」与「改造前」行为完全相同。
func TestDefaultResolvers(t *testing.T) {
	if DefaultStoragePoolFallback != "vmops" {
		t.Errorf("DefaultStoragePoolFallback = %q, want vmops", DefaultStoragePoolFallback)
	}
	if VNCTokenTTLDefaultMin != 5 {
		t.Errorf("VNCTokenTTLDefaultMin = %d, want 5", VNCTokenTTLDefaultMin)
	}
	if VNCStaleDefaultMin != 60 {
		t.Errorf("VNCStaleDefaultMin = %d, want 60", VNCStaleDefaultMin)
	}
}
