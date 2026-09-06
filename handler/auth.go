package handler

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// AuthHandler 认证处理器
type AuthHandler struct {
	DB *gorm.DB
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(db *gorm.DB) *AuthHandler {
	return &AuthHandler{DB: db}
}

// loginLimiter 登录失败限流器（内存实现，按来源 IP 计数）。
// 策略：同一 IP 在统计窗口内登录失败达到上限后锁定到窗口结束，成功登录即清零。
// 目的：抬高登录接口的口令爆破成本（纯内存，重启即重置，不引入外部依赖）。
type loginLimiter struct {
	mu       sync.Mutex
	fails    map[string]*loginFailRecord
	window   time.Duration // 统计窗口
	maxFails int           // 窗口内允许的最大失败次数
}

type loginFailRecord struct {
	count   int
	resetAt time.Time
}

// loginLimiterDefault 登录限流默认实例：1 分钟窗口内失败 5 次即锁定。
var loginLimiterDefault = newLoginLimiter(time.Minute, 5)

func newLoginLimiter(window time.Duration, maxFails int) *loginLimiter {
	return &loginLimiter{
		fails:    map[string]*loginFailRecord{},
		window:   window,
		maxFails: maxFails,
	}
}

// blocked 判断该 IP 是否处于锁定状态；锁定时返回剩余等待时长。
func (l *loginLimiter) blocked(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.fails[ip]
	if !ok {
		return false, 0
	}
	if time.Now().After(rec.resetAt) {
		delete(l.fails, ip) // 窗口已过，惰性清理
		return false, 0
	}
	if rec.count >= l.maxFails {
		return true, time.Until(rec.resetAt)
	}
	return false, 0
}

// fail 记录一次失败；窗口内首次失败起算，达到上限即锁定。顺带惰性清理过期项防膨胀。
func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.fails) > 1024 { // 简单防膨胀：规模超阈值时先清一轮过期项
		for k, rec := range l.fails {
			if now.After(rec.resetAt) {
				delete(l.fails, k)
			}
		}
	}
	rec, ok := l.fails[ip]
	if !ok || now.After(rec.resetAt) {
		l.fails[ip] = &loginFailRecord{count: 1, resetAt: now.Add(l.window)}
		return
	}
	rec.count++
}

// success 登录成功清零该 IP 计数。
func (l *loginLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	AccessToken string      `json:"access_token"`
	TokenType   string      `json:"token_type"`
	User        *model.User `json:"user"`
}

// Login 用户登录（带失败限流：同 IP 1 分钟内失败 5 次锁定 1 分钟，成功清零）
func (h *AuthHandler) Login(c *gin.Context) {
	ip := c.ClientIP()
	if blocked, wait := loginLimiterDefault.blocked(ip); blocked {
		c.Header("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		Fail(c, http.StatusTooManyRequests, "登录失败次数过多，请约 1 分钟后再试")
		return
	}

	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "用户名或密码不能为空")
		return
	}

	// 查询用户
	var user model.User
	if err := h.DB.Where("username = ?", req.Username).First(&user).Error; err != nil {
		loginLimiterDefault.fail(ip)
		Fail(c, http.StatusBadRequest, "用户名或密码错误")
		return
	}

	// 验证密码
	if !middleware.CheckPassword(req.Password, user.PasswordHash) {
		loginLimiterDefault.fail(ip)
		Fail(c, http.StatusBadRequest, "用户名或密码错误")
		return
	}

	// 检查用户状态
	if !user.IsActive {
		Fail(c, http.StatusForbidden, "账号已被禁用")
		return
	}

	// 登录成功：清零失败计数
	loginLimiterDefault.success(ip)

	// 生成Token
	token, err := middleware.GenerateToken(&user)
	if err != nil {
		Fail(c, http.StatusInternalServerError, "生成Token失败")
		return
	}

	// 更新最后登录时间
	now := time.Now()
	h.DB.Model(&user).Update("last_login", &now)

	Created(c, "登录成功", LoginResponse{
		AccessToken: token,
		TokenType:   "bearer",
		User:        &user,
	})
}

// GetMe 获取当前用户信息
func (h *AuthHandler) GetMe(c *gin.Context) {
	userID, _ := c.Get("user_id")

	var user model.User
	if err := h.DB.First(&user, userID).Error; err != nil {
		Fail(c, http.StatusNotFound, "用户不存在")
		return
	}

	Success(c, user)
}
