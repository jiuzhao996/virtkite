package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// ciSpecJSON 把对象序列化为 RawMessage（测试辅助，失败直接 panic 终止用例）。
// 命名避开 alert_webhook.go 已有的 mustJSON(map[string]string) string。
func ciSpecJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return b
}

// TestBuildTemplateRow 请求体 → 模型行的规整与校验（v3 批次 L）。
func TestBuildTemplateRow(t *testing.T) {
	full := map[string]interface{}{
		"hostname": " lab-vm ",
		"user":     "ubuntu",
		"password": "secret123",
		"ssh_key":  "ssh-ed25519 AAAAC3Nza key",
		"net_mode": "static",
		"ip":       "192.168.122.10",
		"gateway":  "192.168.122.1",
		"dns":      []string{"114.114.114.114", "8.8.8.8"},
	}
	t.Run("完整合法 spec 落库并规整", func(t *testing.T) {
		row, spec, err := buildTemplateRow(model.CloudInitTemplate{}, cloudInitTemplateReq{
			Name:        "  教学实验室模板  ",
			Spec:        ciSpecJSON(t, full),
			Description: " ubuntu 24.04 专用 ",
		})
		if err != nil {
			t.Fatalf("期望成功，得到错误: %v", err)
		}
		if row.Name != "教学实验室模板" {
			t.Errorf("名称应去首尾空白，得到 %q", row.Name)
		}
		if row.Description != "ubuntu 24.04 专用" {
			t.Errorf("描述应去首尾空白，得到 %q", row.Description)
		}
		if spec.Hostname != "lab-vm" {
			t.Errorf("hostname 应去首尾空白，得到 %q", spec.Hostname)
		}
		// 落库的 Spec 必须能无损还原为对象（对外契约：出库即对象）
		var back virt.CloudInitSpec
		if err := json.Unmarshal([]byte(row.Spec), &back); err != nil {
			t.Fatalf("落库 Spec 应为合法 JSON: %v", err)
		}
		if len(back.DNS) != 2 || back.IP != "192.168.122.10" {
			t.Errorf("落库 Spec 字段不完整: %+v", back)
		}
	})
	t.Run("net_mode 空归一化为 dhcp", func(t *testing.T) {
		_, spec, err := buildTemplateRow(model.CloudInitTemplate{}, cloudInitTemplateReq{
			Name: "t",
			Spec: ciSpecJSON(t, map[string]interface{}{}),
		})
		if err != nil {
			t.Fatalf("空 spec（全默认）应合法: %v", err)
		}
		if spec.NetMode != "dhcp" {
			t.Errorf("net_mode 空应归一化为 dhcp，得到 %q", spec.NetMode)
		}
	})
	cases := []struct {
		name string
		req  cloudInitTemplateReq
	}{
		{"名称为空", cloudInitTemplateReq{Name: "   ", Spec: ciSpecJSON(t, map[string]interface{}{})}},
		{"名称超长", cloudInitTemplateReq{Name: strings.Repeat("模", 101), Spec: ciSpecJSON(t, map[string]interface{}{})}},
		{"spec 缺失", cloudInitTemplateReq{Name: "t"}},
		{"spec 非对象", cloudInitTemplateReq{Name: "t", Spec: json.RawMessage(`["dhcp"]`)}},
		{"spec 未知字段", cloudInitTemplateReq{Name: "t", Spec: json.RawMessage(`{"evil":"x","user":"u"}`)}},
		{"描述超长", cloudInitTemplateReq{Name: "t", Spec: ciSpecJSON(t, map[string]interface{}{}), Description: strings.Repeat("述", 501)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := buildTemplateRow(model.CloudInitTemplate{}, tc.req); err == nil {
				t.Errorf("期望校验失败，实际通过")
			}
		})
	}
}

// TestValidateTemplateSpecSpec 字段级校验规则（错误均为固定中文文案可直接回显）。
func TestValidateTemplateSpecFields(t *testing.T) {
	badCases := []struct {
		name string
		mut  func(*virt.CloudInitSpec)
	}{
		{"hostname 非法字符", func(s *virt.CloudInitSpec) { s.Hostname = "bad host" }},
		{"hostname 连字符开头", func(s *virt.CloudInitSpec) { s.Hostname = "-vm" }},
		{"user 非法字符", func(s *virt.CloudInitSpec) { s.User = "ro ot" }},
		{"password 含换行（可注入 cloud-config 键）", func(s *virt.CloudInitSpec) { s.Password = "x\nsudo: ALL=(ALL) NOPASSWD:ALL" }},
		{"ssh_key 含换行", func(s *virt.CloudInitSpec) { s.SSHKey = "key\nssh_authorized_keys: evil" }},
		{"password 超 128", func(s *virt.CloudInitSpec) { s.Password = strings.Repeat("p", 129) }},
		{"net_mode 非白名单", func(s *virt.CloudInitSpec) { s.NetMode = "pppoe" }},
		{"static 缺 ip", func(s *virt.CloudInitSpec) { s.NetMode = "static"; s.Gateway = "192.168.122.1" }},
		{"static 缺 gateway", func(s *virt.CloudInitSpec) { s.NetMode = "static"; s.IP = "192.168.122.10" }},
		{"ip 非法", func(s *virt.CloudInitSpec) { s.NetMode = "static"; s.IP = "999.1.1.1"; s.Gateway = "192.168.122.1" }},
		{"ip 为 IPv6 映射写法", func(s *virt.CloudInitSpec) {
			s.NetMode = "static"
			s.IP = "::ffff:192.168.122.10"
			s.Gateway = "192.168.122.1"
		}},
		{"gateway 非法", func(s *virt.CloudInitSpec) { s.NetMode = "static"; s.IP = "192.168.122.10"; s.Gateway = "not-an-ip" }},
		{"dns 非法", func(s *virt.CloudInitSpec) { s.DNS = []string{"8.8.8.8", "dns.example.com"} }},
		{"dns 超 8 个", func(s *virt.CloudInitSpec) {
			s.DNS = []string{"1.1.1.1", "1.1.1.2", "1.1.1.3", "1.1.1.4", "1.1.1.5", "1.1.1.6", "1.1.1.7", "1.1.1.8", "1.1.1.9"}
		}},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &virt.CloudInitSpec{NetMode: "dhcp"}
			tc.mut(spec)
			if err := validateTemplateSpec(spec); err == nil {
				t.Errorf("期望校验失败，实际通过: %+v", spec)
			}
		})
	}
	t.Run("dhcp 允许缺省 ip/gateway", func(t *testing.T) {
		spec := &virt.CloudInitSpec{NetMode: "dhcp"}
		if err := validateTemplateSpec(spec); err != nil {
			t.Fatalf("dhcp 模式 ip/gateway 应可空: %v", err)
		}
	})
	t.Run("dns 去重去空并 trim", func(t *testing.T) {
		spec := &virt.CloudInitSpec{NetMode: "dhcp", DNS: []string{" 8.8.8.8 ", "8.8.8.8", "", "114.114.114.114"}}
		if err := validateTemplateSpec(spec); err != nil {
			t.Fatalf("期望成功: %v", err)
		}
		if len(spec.DNS) != 2 || spec.DNS[0] != "8.8.8.8" {
			t.Errorf("dns 应去重去空，得到 %v", spec.DNS)
		}
	})
}

// TestValidateTemplateName 模板名校验。
func TestValidateTemplateName(t *testing.T) {
	if name, err := validateTemplateName(" 实验模板 "); err != nil || name != "实验模板" {
		t.Errorf("合法名称应通过并去空白，得到 (%q, %v)", name, err)
	}
	if _, err := validateTemplateName(""); err == nil {
		t.Error("空名称应被拒绝")
	}
	if _, err := validateTemplateName(strings.Repeat("a", 101)); err == nil {
		t.Error("超 100 字符名称应被拒绝")
	}
}
