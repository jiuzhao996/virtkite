package middleware

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/wsticket"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Claims JWT声明
type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// GenerateToken 生成JWT Token
func GenerateToken(user *model.User) (string, error) {
	claims := Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(config.GlobalConfig.JWTExpireMinutes) * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.GlobalConfig.JWTSecretKey))
}

// ParseToken 解析JWT Token
func ParseToken(tokenString string) (*Claims, error) {
	// 必须用 WithValidMethods 锁定签名算法：否则攻击者可把 header 的 alg 换成
	// 其他类型让库走不同的验签路径（算法混淆攻击）。本项目只签发 HS256。
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(config.GlobalConfig.JWTSecretKey), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, jwt.ErrSignatureInvalid
}

// abortJSON 以全站统一响应格式 {code, message, data} 中断请求。
// 中间件不能复用 handler 包的 Fail：handler 已依赖 middleware（HashPassword 等），
// 反向引用会构成导入环，因此在本包内单独提供。
func abortJSON(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"code":    status,
		"message": message,
		"data":    nil,
	})
}

// claimsFromRequest 从 HTTP 请求的 Authorization 头解析 JWT Claims。
// 供全局注册的审计中间件使用（其执行时机早于 AuthMiddleware，需自行补全用户身份）。
//
// 只认标准 Authorization 头：WebSocket 的 ?token= 兼容分支已移除（改用一次性短时票据，
// 见 service/wsticket 与 AuthMiddleware 的票据分支）；且 GET 本就不入审计，WS 也无需此路径。
func claimsFromRequest(req *http.Request) (*Claims, error) {
	authHeader := req.Header.Get("Authorization")
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
		return nil, fmt.Errorf("无有效认证信息")
	}
	return ParseToken(parts[1])
}

// HashPassword 哈希密码
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword 验证密码
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// applyUserIdentity 查库取用户、校验状态、刷新最后登录时间并写入 gin 上下文。
// 返回 false 表示已写响应并中断，调用方直接 return。两条认证路径（JWT 头 / WS 票据）
// 共用，避免身份处理逻辑分叉。
func applyUserIdentity(c *gin.Context, db *gorm.DB, userID uint) bool {
	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		abortJSON(c, http.StatusUnauthorized, "用户不存在")
		return false
	}
	if !user.IsActive {
		abortJSON(c, http.StatusForbidden, "账号已被禁用")
		return false
	}

	// 更新最后登录时间（best-effort）：审计时间写不进去只降级为缺一条记录，
	// 因此仅记日志、不阻断登录；静默吞掉错误则数据库故障永远不会被发现。
	now := time.Now()
	if err := db.Model(&user).Update("last_login", &now).Error; err != nil {
		log.Printf("[auth] 记录最后登录时间失败 user=%s err=%v", user.Username, err)
	}

	c.Set("user_id", user.ID)
	c.Set("username", user.Username)
	c.Set("role", user.Role)
	c.Set("user", &user)
	return true
}

// wsTicketResource 返回当前 WS 请求对应的资源标识，供票据绑定校验（见 service/wsticket）。
// 按注册路由模板 c.FullPath 判定，覆盖 SSH 终端/串口与容器终端/日志流四类；
// 其它路径返回空串——空串永不匹配已签发票据（票据 resource 非空），安全兜底。
func wsTicketResource(c *gin.Context) string {
	full := c.FullPath()
	switch {
	case strings.HasPrefix(full, "/api/vms/") &&
		(strings.HasSuffix(full, "/terminal") || strings.HasSuffix(full, "/serial")):
		return wsticket.VMResource(c.Param("id"))
	case strings.HasPrefix(full, "/api/docker/") &&
		(strings.HasSuffix(full, "/terminal") || strings.HasSuffix(full, "/logs/ws")):
		return wsticket.DockerResource(c.Param("id"))
	}
	return ""
}

// AuthMiddleware 认证中间件，支持两种凭证：
//  1. Authorization: Bearer <JWT>（常规 HTTP 请求，axios 默认带上）；
//  2. ?ticket=<一次性短时票据>（仅 WebSocket：浏览器无法自定义请求头）。
//
// 历史实现的 WebSocket 分支是 ?token=<JWT>（长期凭证进 URL/日志），已由票据取代：
// 票据由 POST /api/vms/:id/ws-ticket 与 POST /api/docker/containers/:id/ws-ticket 签发，
// 单次使用 + 30 秒有效 + 绑定目标资源，泄漏面从「小时级全权限」降到「一张废票」。
func AuthMiddleware(db *gorm.DB, tickets *wsticket.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1) 标准 Authorization 头
		if authHeader := c.GetHeader("Authorization"); authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || parts[0] != "Bearer" {
				abortJSON(c, http.StatusUnauthorized, "认证格式错误")
				return
			}
			claims, err := ParseToken(parts[1])
			if err != nil {
				abortJSON(c, http.StatusUnauthorized, "Token 无效或已过期")
				return
			}
			if !applyUserIdentity(c, db, claims.UserID) {
				return
			}
			c.Next()
			return
		}

		// 2) WebSocket 一次性票据
		if tickets != nil {
			if tk := c.Query("ticket"); tk != "" {
				entry, ok := tickets.Consume(tk)
				if !ok {
					abortJSON(c, http.StatusUnauthorized, "连接凭证无效或已过期")
					return
				}
				if entry.Resource == "" || entry.Resource != wsTicketResource(c) {
					abortJSON(c, http.StatusUnauthorized, "连接凭证与目标不匹配")
					return
				}
				if !applyUserIdentity(c, db, entry.UserID) {
					return
				}
				c.Next()
				return
			}
		}

		abortJSON(c, http.StatusUnauthorized, "未提供认证信息")
	}
}

// AdminMiddleware 管理员权限中间件
func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 断言带 ok：上下文里的 role 类型不符时按无权限处理，不能 panic 掉整条请求链
		role, exists := c.Get("role")
		if roleStr, ok := role.(string); !exists || !ok || roleStr != "admin" {
			abortJSON(c, http.StatusForbidden, "需要管理员权限")
			return
		}
		c.Next()
	}
}

// OperatorMiddleware 运维权限中间件（RBAC 三级角色：admin / operator / viewer）。
// admin 放行全部；operator 可操作虚拟机全生命周期（/api/vms 下所有方法，含三类控制台
// —— SSH 终端/串口是其职责内的操作通道）与全部读操作，其余模块（宿主机/存储/网络/
// 镜像）的写操作一律 403；viewer 只读（GET）+ 图形控制台观看（vnc-token 下发 view_only）。
// /users 与 /audit 组继续用 AdminMiddleware（用户哈希与审计敏感）。
//
// 例外：SSH 终端与串口控制台虽然是 GET，但建立的是对 guest 的**双向写入**通道
// （/terminal 是 SSH shell，/serial 直连虚拟机串口，多数云镜像上即 root TTY），
// 与「只读」语义冲突，因此对 viewer 关闭——operator 因操作职责正常放行。
func OperatorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("role")
		if roleStr, ok := role.(string); ok && roleStr == "admin" {
			c.Next()
			return
		}
		// operator：读全放；写仅限 /api/vms 前缀（虚拟机全生命周期，含 vnc-token）
		if roleStr, ok := role.(string); ok && roleStr == "operator" {
			if c.Request.Method == http.MethodGet {
				c.Next()
				return
			}
			if strings.HasPrefix(c.FullPath(), "/api/vms") {
				c.Next()
				return
			}
			abortJSON(c, http.StatusForbidden, "仅可操作虚拟机，宿主机/存储/网络/镜像的变更需要管理员权限")
			return
		}
		// viewer 只读 + 图形控制台观看
		if c.Request.Method == http.MethodGet {
			if isGuestWriteChannel(c.FullPath()) {
				abortJSON(c, http.StatusForbidden, "只读角色不能使用 SSH 终端与串口控制台，请使用图形控制台查看")
				return
			}
			c.Next()
			return
		}
		// POST /api/vms/:id/vnc-token（图形控制台查看）
		if c.Request.Method == http.MethodPost && isVNCTokenPath(c.FullPath()) {
			c.Next()
			return
		}
		abortJSON(c, http.StatusForbidden, "需要管理员权限")
	}
}

// NonViewerMiddleware 监控可见级别闸（全量审计 P0：file-sd 会枚举全部 running VM 的名称与 IP，
// 实时告警 labels 同样携带资产标识——viewer 不得见）。operator/admin 放行，viewer 一律 403。
func NonViewerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("role")
		if roleStr, ok := role.(string); ok && (roleStr == "admin" || roleStr == "operator") {
			c.Next()
			return
		}
		abortJSON(c, http.StatusForbidden, "监控数据仅操作员与管理员可见")
	}
}

// isVNCTokenPath 判断是否为 VNC token 签发路径（gin FullPath 模板，如 /api/vms/:id/vnc-token）。
func isVNCTokenPath(fullPath string) bool {
	return strings.HasSuffix(fullPath, "/vnc-token")
}

// isGuestWriteChannel 判断是否为对 guest 的交互式写入通道（SSH 终端 / 串口控制台）。
// 两者都注册为 GET（浏览器 WebSocket 只能发 GET），不能只靠方法判断读写语义。
func isGuestWriteChannel(fullPath string) bool {
	return strings.HasSuffix(fullPath, "/terminal") || strings.HasSuffix(fullPath, "/serial")
}
