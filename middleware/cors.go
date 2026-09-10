package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
)

// CORSMiddleware CORS中间件。
// CORS_ORIGINS 支持逗号分隔多来源；按请求 Origin 白名单精确匹配后回显——
// 规范要求 ACAO 只能是单个 origin 或 *，把 "a,b" 整串塞回去浏览器会直接拒绝。
// 特殊值 * 保持整串返回（允许任意来源，仅限 debug 模式，release 启动校验会拒绝）。
func CORSMiddleware() gin.HandlerFunc {
	allowed := func(origin string) bool {
		for _, o := range strings.Split(config.GlobalConfig.CORSOrigins, ",") {
			if strings.TrimSpace(o) == origin {
				return true
			}
		}
		return false
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if config.GlobalConfig.CORSOrigins == "*" || allowed(origin) {
				c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
				c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
				c.Writer.Header().Set("Vary", "Origin")
			}
			c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Authorization, Accept, X-Requested-With")
			c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
