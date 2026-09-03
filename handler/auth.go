package handler

import (
	"net/http"
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

// Login 用户登录
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "用户名或密码不能为空")
		return
	}

	// 查询用户
	var user model.User
	if err := h.DB.Where("username = ?", req.Username).First(&user).Error; err != nil {
		Fail(c, http.StatusBadRequest, "用户名或密码错误")
		return
	}

	// 验证密码
	if !middleware.CheckPassword(req.Password, user.PasswordHash) {
		Fail(c, http.StatusBadRequest, "用户名或密码错误")
		return
	}

	// 检查用户状态
	if !user.IsActive {
		Fail(c, http.StatusForbidden, "账号已被禁用")
		return
	}

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
