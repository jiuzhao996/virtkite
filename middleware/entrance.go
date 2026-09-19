package middleware

// 安全入口中间件（v3 批次 D）：给登录接口加一道「暗号」门。
//
// 背景：登录接口是全平台唯一无需认证的写入口（口令爆破面），登录限流之外再加一层
// 隐蔽性防护——设置 security_entrance 后，请求必须携带匹配的暗号才可能到达登录逻辑，
// 不匹配一律按 404 处理（与 gin 默认 404 响应体一致，不泄露「此端点存在但被拦」的信息）。
//
// 与 handler 层校验的关系：AuthHandler.Login 内部还有一处动态读配置的同语义校验
// （SettingMgr 每请求实时读，设置改完即时生效）；本中间件值来自挂载时的快照（改配置需重启）。
// 二者叠加为纵深防御；EntranceMiddleware/EntranceMatch/EntranceFromRequest 供两条路径复用同一套语义。

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

// EntranceHeader 登录安全入口的请求头名。
const EntranceHeader = "X-Entrance"

// EntranceQuery 登录安全入口的查询参数名（请求头未携带时的兜底通道）。
const EntranceQuery = "entrance"

// EntranceMatch 判断请求携带的暗号是否匹配预期（expected 为空 = 入口关闭，恒匹配）。
// 比较用 subtle.ConstantTimeCompare 常量时间进行，防止按字节逐位比对的时序侧信道逐字符猜出暗号。
func EntranceMatch(expected, provided string) bool {
	if expected == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// EntranceFromRequest 提取请求携带的暗号：X-Entrance 头优先，为空再取 ?entrance= 查询参数。
func EntranceFromRequest(c *gin.Context) string {
	provided := c.GetHeader(EntranceHeader)
	if provided == "" {
		provided = c.Query(EntranceQuery)
	}
	return provided
}

// EntranceMiddleware 安全入口校验中间件（只挂载到需要保护的单独路由，规划挂载点 POST /api/auth/login）。
// entrance 为空 = 入口关闭，直接放行；非空时请求须携带匹配的 X-Entrance 头或 ?entrance= 查询参数，
// 不匹配一律 404（响应体与 gin 默认 404 完全一致，不泄露端点存在性）。
func EntranceMiddleware(entrance string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !EntranceMatch(entrance, EntranceFromRequest(c)) {
			// 与 gin 引擎对不存在路由的默认响应（"404 page not found"）保持一致，
			// 让被拦请求与访问了不存在路径在响应层面不可区分
			c.String(http.StatusNotFound, "404 page not found")
			c.Abort()
			return
		}
		c.Next()
	}
}
