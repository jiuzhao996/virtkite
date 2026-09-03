package handler

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// UserHandler 用户处理器
type UserHandler struct {
	DB *gorm.DB
}

// NewUserHandler 创建用户处理器
func NewUserHandler(db *gorm.DB) *UserHandler {
	return &UserHandler{DB: db}
}

// ListUsers 获取用户列表
func (h *UserHandler) ListUsers(c *gin.Context) {
	var users []model.User
	if err := h.DB.Find(&users).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "查询用户失败")
		return
	}

	Success(c, gin.H{
		"total": len(users),
		"items": users,
	})
}

// CreateUser 创建用户
func (h *UserHandler) CreateUser(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		Role     string `json:"role"`
		RealName string `json:"real_name"`
		Phone    string `json:"phone"`
		Email    string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}

	// 哈希密码
	passwordHash, err := middleware.HashPassword(req.Password)
	if err != nil {
		Fail(c, http.StatusInternalServerError, "密码处理失败")
		return
	}

	// 设置默认角色
	if req.Role == "" {
		req.Role = "viewer"
	}

	// 创建用户
	user := model.User{
		Username:     req.Username,
		PasswordHash: passwordHash,
		Role:         req.Role,
		RealName:     req.RealName,
		Phone:        req.Phone,
		Email:        req.Email,
		IsActive:     true,
	}

	if err := h.DB.Create(&user).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "创建用户失败")
		return
	}

	Created(c, "创建成功", user)
}

// UpdateUser 更新用户
func (h *UserHandler) UpdateUser(c *gin.Context) {
	id := c.Param("id")

	var user model.User
	if err := h.DB.First(&user, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "用户不存在")
		return
	}

	var req struct {
		Role     *string `json:"role"`
		IsActive *bool   `json:"is_active"`
		RealName *string `json:"real_name"`
		Phone    *string `json:"phone"`
		Email    *string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}

	// 更新字段
	updates := map[string]interface{}{}
	if req.Role != nil {
		updates["role"] = *req.Role
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}
	if req.RealName != nil {
		updates["real_name"] = *req.RealName
	}
	if req.Phone != nil {
		updates["phone"] = *req.Phone
	}
	if req.Email != nil {
		updates["email"] = *req.Email
	}

	if err := h.DB.Model(&user).Updates(updates).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "更新用户失败")
		return
	}

	Created(c, "更新成功", nil)
}

// DeleteUser 删除用户
func (h *UserHandler) DeleteUser(c *gin.Context) {
	id := c.Param("id")

	// 不能删除自己
	currentUserID, _ := c.Get("user_id")
	var userID uint
	if _, ok := currentUserID.(uint); ok {
		userID = currentUserID.(uint)
	}

	var targetID uint
	if _, err := fmt.Sscanf(id, "%d", &targetID); err != nil {
		Fail(c, http.StatusBadRequest, "无效的用户ID")
		return
	}

	if userID == targetID {
		Fail(c, http.StatusBadRequest, "不能删除自己")
		return
	}

	var user model.User
	if err := h.DB.First(&user, targetID).Error; err != nil {
		Fail(c, http.StatusNotFound, "用户不存在")
		return
	}

	if err := h.DB.Delete(&user).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "删除用户失败")
		return
	}

	Created(c, "删除成功", nil)
}
