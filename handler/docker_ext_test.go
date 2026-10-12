package handler

import (
	"net/http"
	"testing"
)

// TestDockerValidateComposeRequiresAdmin 覆盖 compose 校验端点的 admin 收口。
//
// 风险点：docker compose config 会解析 include / env_file 等本地文件引用，
// 属文件读取面，故与 stacks.File 同口径仅管理员可用。角色闸在 dockerAvailable
// 与 bindJSON 之前，本测试无需 docker 依赖（零值 handler 不解引用 h.Docker）。
func TestDockerValidateComposeRequiresAdmin(t *testing.T) {
	cases := []struct {
		name string
		role string
		set  bool
	}{
		{"operator 被拒", "operator", true},
		{"viewer 被拒", "viewer", true},
		{"角色缺失被拒", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newTestContext("/api/docker/compose/validate", nil)
			if tc.set {
				c.Set("role", tc.role)
			}
			h := &DockerHandler{}
			h.ValidateCompose(c)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("期望 403，实际 %d，响应体=%s", rec.Code, rec.Body.String())
			}
			body := decodeBody(t, rec)
			if msg, _ := body["message"].(string); msg != "compose 校验仅管理员可用" {
				t.Errorf("文案不符：%q", msg)
			}
		})
	}
}
