package middleware

import "github.com/gin-gonic/gin"

// SecurityHeadersMiddleware 基础安全响应头。
//
// 刻意不加 CSP：前端依赖 Element Plus 的内联样式与 Vite 产物，严格 CSP 会直接
// 破坏页面渲染；HSTS 也不加（本平台允许纯 HTTP 内网部署）。这里只放不影响功能、
// 又能挡住常见浏览器侧攻击的头：
//   - nosniff：阻止浏览器把 JSON/文本嗅探成脚本执行；
//   - X-Frame-Options：禁止第三方站点 iframe 本平台（防点击劫持）；
//   - Referrer-Policy：跨站请求不携带路径（避免 token/资源 ID 经 Referer 外泄）；
//   - Permissions-Policy：关闭平台不使用的设备权限。
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Next()
	}
}
