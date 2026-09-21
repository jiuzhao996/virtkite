package handler

import (
	"net/http"
	"testing"
)

// TestCreateContainerRequiresAdmin 覆盖容器创建端点的 admin 二次收口。
//
// 风险点：docker 路由组挂 NonViewerMiddleware 只挡 viewer，operator 的写请求能直达
// 本端点，而创建容器可挂载宿主机任意路径（-v /:/host 即整机文件系统可读改），等于提权。
// 角色闸在 dockerAvailable 探测与 bindJSON 之前，本测试无需 docker 依赖：
// 非 admin 应在触碰 docker 前被拒（零值 handler 不解引用 h.Docker）。
func TestCreateContainerRequiresAdmin(t *testing.T) {
	cases := []struct {
		name string
		role string
		set  bool // 是否写入 role（模拟中间件未写入的异常路径）
	}{
		{"operator 被拒", "operator", true},
		{"viewer 被拒", "viewer", true},
		{"角色缺失（未过鉴权中间件的防御路径）被拒", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newTestContext("/api/docker/containers", nil)
			if tc.set {
				c.Set("role", tc.role)
			}
			// 管理闸位于 dockerAvailable/bindJSON 之前，零值 handler 不会触碰 Docker
			h := &DockerHandler{}
			h.CreateContainer(c)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("期望 403，实际 %d，响应体=%s", rec.Code, rec.Body.String())
			}
			body := decodeBody(t, rec)
			if body["code"].(float64) != float64(http.StatusForbidden) {
				t.Errorf("业务 code 期望 403，实际 %v", body["code"])
			}
			msg, _ := body["message"].(string)
			if msg != "容器创建仅管理员可用" {
				t.Errorf("文案不符：%q", msg)
			}
		})
	}
}
