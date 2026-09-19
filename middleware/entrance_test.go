package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newEntranceRouter 组装带安全入口中间件的测试路由；next 标记请求是否到达业务 handler。
func newEntranceRouter(entrance string, reached *bool) *gin.Engine {
	r := gin.New()
	r.POST("/api/auth/login", EntranceMiddleware(entrance), func(c *gin.Context) {
		*reached = true
		c.Status(http.StatusOK)
	})
	return r
}

// TestEntranceMiddleware 安全入口中间件：关闭态放行、匹配放行、不匹配一律 404。
//
// 风险点：这是登录接口的外层门禁。关闭态必须完全放行（否则默认部署登录直接不可用）；
// 不匹配必须 404 且请求不得到达登录逻辑（否则暗号形同虚设）；响应体须与 gin 默认 404
// 一致，让「被拦」与「路径不存在」在响应层面不可区分（不泄露端点存在性）。
func TestEntranceMiddleware(t *testing.T) {
	cases := []struct {
		name        string
		entrance    string // 中间件配置的暗号（空=关闭）
		header      string // X-Entrance 头
		query       string // ?entrance= 参数
		wantReached bool   // 是否应到达业务 handler
	}{
		{"关闭态无任何暗号放行", "", "", "", true},
		{"关闭态带错暗号也放行", "", "wrong", "wrong", true},
		{"头部匹配放行", "k7x2m", "k7x2m", "", true},
		{"查询参数匹配放行", "k7x2m", "", "k7x2m", true},
		{"头部缺失且无查询参数拒绝", "k7x2m", "", "", false},
		{"头部不匹配拒绝", "k7x2m", "wrong", "", false},
		{"查询参数不匹配拒绝", "k7x2m", "", "wrong", false},
		{"头部优先于查询参数（头部错即拒）", "k7x2m", "wrong", "k7x2m", false},
		{"暗号大小写敏感", "k7x2m", "K7X2M", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			r := newEntranceRouter(tc.entrance, &reached)

			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			if tc.header != "" {
				req.Header.Set("X-Entrance", tc.header)
			}
			if tc.query != "" {
				req.URL.RawQuery = "entrance=" + tc.query
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if reached != tc.wantReached {
				t.Errorf("请求到达业务 handler = %v, want %v", reached, tc.wantReached)
			}
			if tc.wantReached {
				if w.Code != http.StatusOK {
					t.Errorf("放行请求 status = %d, want 200", w.Code)
				}
				return
			}
			// 被拦必须与 gin 默认 404 完全一致（同码同响应体），不泄露端点存在性
			if w.Code != http.StatusNotFound {
				t.Errorf("拦截请求 status = %d, want 404", w.Code)
			}
			if body := w.Body.String(); body != "404 page not found" {
				t.Errorf("拦截响应体 = %q, want 与 gin 默认 404 一致的 \"404 page not found\"", body)
			}
		})
	}
}

// TestEntranceMatch 常量时间比较函数语义：空预期恒匹配、精确匹配、大小写与长度差异均不匹配。
func TestEntranceMatch(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		provided string
		want     bool
	}{
		{"空预期=入口关闭恒匹配", "", "anything", true},
		{"空预期空输入匹配", "", "", true},
		{"完全一致匹配", "k7x2m", "k7x2m", true},
		{"大小写不同不匹配", "k7x2m", "k7x2M", false},
		{"预期为空串输入非空不匹配", "secret", "", false},
		{"输入多一个字符不匹配", "secret", "secrets", false},
		{"输入少一个字符不匹配", "secret", "secre", false},
		{"完全不同不匹配", "secret", "xxxxxx", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EntranceMatch(tc.expected, tc.provided); got != tc.want {
				t.Errorf("EntranceMatch(%q, %q) = %v, want %v", tc.expected, tc.provided, got, tc.want)
			}
		})
	}
}
