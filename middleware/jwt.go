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
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(config.GlobalConfig.JWTSecretKey), nil
	})
	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, jwt.ErrSignatureInvalid
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
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未提供认证信息"})
			c.Abort()
			return
		}

		// 提取Token
		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "认证格式错误"})
			c.Abort()
			return
		}

		tokenString := parts[1]

		// 解析Token
		claims, err := ParseToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token无效或已过期"})
			c.Abort()
			return
		}

		// 查询用户
		var user model.User
		if err := db.First(&user, claims.UserID).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户不存在"})
			c.Abort()
			return
		}

		// 检查用户状态
		if !user.IsActive {
			c.JSON(http.StatusForbidden, gin.H{"error": "账号已被禁用"})
			c.Abort()
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
		role, exists := c.Get("role")
		if !exists || role.(string) != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// OperatorMiddleware 运维权限中间件（RBAC 第一阶段：只读 viewer）。
// admin 放行全部；viewer 仅放行读操作（GET）与控制台只看通道（VNC token 签发，
// WS 终端/串口本身就是 GET），其余变更操作（POST/PUT/DELETE）一律 403。
// /users 与 /audit 组继续用 AdminMiddleware（用户哈希与审计敏感）。
func OperatorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("role")
		if roleStr, ok := role.(string); ok && roleStr == "admin" {
			c.Next()
			return
		}
		// viewer 只读 + 控制台
		if c.Request.Method == http.MethodGet {
			c.Next()
			return
		}
		// POST /api/vms/:id/vnc-token（图形控制台查看）
		if c.Request.Method == http.MethodPost && isVNCTokenPath(c.FullPath()) {
			c.Next()
			return
		}
		c.JSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
		c.Abort()
	}
}

// isVNCTokenPath 判断是否为 VNC token 签发路径（gin FullPath 模板，如 /api/vms/:id/vnc-token）。
func isVNCTokenPath(fullPath string) bool {
	return strings.HasSuffix(fullPath, "/vnc-token")
}
