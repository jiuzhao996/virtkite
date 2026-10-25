package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeadersMiddleware())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s 期望 %q，实际 %q", k, v, got)
		}
	}
	if got := w.Header().Get("Permissions-Policy"); got == "" {
		t.Error("Permissions-Policy 不应为空")
	}
	// 刻意不加 CSP：页面依赖内联样式，加了会白屏；用用例钉住这个决策
	if got := w.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("本平台不应启用 CSP（会破坏 Element Plus 内联样式），实际 %q", got)
	}
}
