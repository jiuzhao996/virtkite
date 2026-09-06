package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
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

// claimsFromRequest 从 HTTP 请求的 Authorization 头（或 ?token= 查询参数）解析 JWT Claims。
// 供全局注册的审计中间件使用（其执行时机早于 AuthMiddleware，需自行补全用户身份）。
func claimsFromRequest(req *http.Request) (*Claims, error) {
	authHeader := req.Header.Get("Authorization")
	if authHeader == "" {
		authHeader = "Bearer " + req.URL.Query().Get("token")
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if !(len(parts) == 2 && parts[0] == "Bearer") || parts[1] == "" {
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

// AuthMiddleware JWT认证中间件
func AuthMiddleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取Authorization头
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// WebSocket 无法设置 Header，允许通过 ?token= 传递 JWT
			authHeader = "Bearer " + c.Query("token")
		}
		if authHeader == "Bearer " {
			abortJSON(c, http.StatusUnauthorized, "未提供认证信息")
			return
		}

		// 提取Token
		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			abortJSON(c, http.StatusUnauthorized, "认证格式错误")
			return
		}

		tokenString := parts[1]

		// 解析Token
		claims, err := ParseToken(tokenString)
		if err != nil {
			abortJSON(c, http.StatusUnauthorized, "Token 无效或已过期")
			return
		}

		// 查询用户
		var user model.User
		if err := db.First(&user, claims.UserID).Error; err != nil {
			abortJSON(c, http.StatusUnauthorized, "用户不存在")
			return
		}

		// 检查用户状态
		if !user.IsActive {
			abortJSON(c, http.StatusForbidden, "账号已被禁用")
			return
		}

		// 更新最后登录时间
		now := time.Now()
		db.Model(&user).Update("last_login", &now)

		// 将用户信息存储到上下文
		c.Set("user_id", user.ID)
		c.Set("username", user.Username)
		c.Set("role", user.Role)
		c.Set("user", &user)

		c.Next()
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

// OperatorMiddleware 运维权限中间件（RBAC 第一阶段：只读 viewer）。
// admin 放行全部；viewer 放行读操作（GET）与图形控制台 token 签发（VNC 以 view_only 模式打开），
// 其余变更操作（POST/PUT/DELETE）一律 403。
// /users 与 /audit 组继续用 AdminMiddleware（用户哈希与审计敏感）。
//
// 例外：SSH 终端与串口控制台虽然是 GET，但建立的是对 guest 的**双向写入**通道
// （/terminal 是 SSH shell，/serial 直连虚拟机串口，多数云镜像上即 root TTY），
// 与「只读」语义冲突，因此对 viewer 关闭，只留图形控制台的只读观看。
func OperatorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("role")
		if roleStr, ok := role.(string); ok && roleStr == "admin" {
			c.Next()
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

// isVNCTokenPath 判断是否为 VNC token 签发路径（gin FullPath 模板，如 /api/vms/:id/vnc-token）。
func isVNCTokenPath(fullPath string) bool {
	return strings.HasSuffix(fullPath, "/vnc-token")
}

// isGuestWriteChannel 判断是否为对 guest 的交互式写入通道（SSH 终端 / 串口控制台）。
// 两者都注册为 GET（浏览器 WebSocket 只能发 GET），不能只靠方法判断读写语义。
func isGuestWriteChannel(fullPath string) bool {
	return strings.HasSuffix(fullPath, "/terminal") || strings.HasSuffix(fullPath, "/serial")
}
