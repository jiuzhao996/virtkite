// user.go：平台用户账号管理（仅 admin）与当前登录用户自助改密。
//
// 职责边界：本文件只负责 HTTP 参数绑定、角色/密码策略校验与 GORM 读写；
// 口令哈希与校验委托 middleware.HashPassword / CheckPassword，密码强度规则委托
// service/setting（支持运行时调整），handler 内不硬编码强度规则。
//
// 安全约定：
//   - 路径主键一律先经 paramID 解析成 uint 再交给 GORM，禁止直传 c.Param
//     （GORM 会把非纯数字字符串当原始 SQL 片段拼接，见 AGENTS 第 8 条）；
//   - 密码哈希列永不序列化给前端（model.User.PasswordHash 带 json:"-"）；
//   - users 属资产表，走 GORM 软删除（DeletedAt），删除后记录仍可追溯。

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/setting"
	"gorm.io/gorm"
)

// legacyPasswordMinLength 改造前 CreateUser/ChangeMyPassword 的硬编码密码下限。
// 仅在 settingMgr 未接线时兜底使用，避免「忘记注入」静默放开已有的口令强度约束。
const legacyPasswordMinLength = 6

// UserHandler 用户处理器（仅挂载在 AdminMiddleware 之后的管理路由上）。
//
// settingMgr 采用「构造后注入」而非构造参数：NewUserHandler 的签名保持不变，
// routes.go 里补一行 SetSettingMgr 即可；未接线时 passwordPolicy 回落到
// legacyPasswordMinLength，避免因漏注入而静默放开口令强度。
type UserHandler struct {
	DB         *gorm.DB
	settingMgr *setting.Manager // 系统可写配置（SetSettingMgr 注入，未注入时用改造前硬底线）
}

// NewUserHandler 创建用户处理器
func NewUserHandler(db *gorm.DB) *UserHandler {
	return &UserHandler{DB: db}
}

// SetSettingMgr 注入系统设置管理器（向后兼容注入：构造器签名不变，routes.go 补一行接线）。
func (h *UserHandler) SetSettingMgr(m *setting.Manager) {
	h.settingMgr = m
}

// passwordPolicy 读取密码最小长度策略（实时读，设置改完即时生效；0=关闭策略）。
// 未注入管理器时退回改造前的 6 位硬底线。
func (h *UserHandler) passwordPolicy() int {
	if h.settingMgr == nil {
		return legacyPasswordMinLength
	}
	return h.settingMgr.PasswordMinLength()
}

// ListUsers 获取用户列表。
//
// 平台用户量级很小（教学/团队场景），因此不分页、一次返回全量；GORM 默认过滤
// 软删除记录。nil 切片显式替换成空切片，保证 JSON 序列化为 [] 而不是 null，
// 前端表格可直接渲染。
func (h *UserHandler) ListUsers(c *gin.Context) {
	var users []model.User
	if err := h.DB.Find(&users).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询用户失败", err)
		return
	}
	if users == nil {
		users = []model.User{}
	}

	Success(c, gin.H{
		"total": len(users),
		"items": users,
	})
}

// validRoles 角色白名单，与 model.User.Role 的取值一致：
// admin（管理员，全部权限）/ operator（运维，可操作资源）/ viewer（只读）。
// 用白名单而非「非空即合法」，避免拼写错误或未知角色被写入后在鉴权处产生歧义。
var validRoles = map[string]bool{"admin": true, "operator": true, "viewer": true}

// CreateUser 创建用户（POST /api/users，仅 admin）。
//
// 校验顺序：请求体 → 密码策略 → 角色白名单 → 哈希 → 入库。用户名唯一性由
// users.username 的唯一索引兜底；冲突时统一返回「创建用户失败」，不把底层
// 数据库错误泄漏给前端。注意（既有实现）：这里用的是 Fail 而非 ErrorResponse，
// 底层错误不会进日志，排查唯一键冲突需依赖数据库侧日志。
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

	// 密码策略（v3 批次 D）：长度按系统设置 password_min_length 校验（策略 ≥8 时
	// 额外要求字母+数字混合）。错误文案是固定中文，可直接回显给前端。
	if err := setting.ValidatePassword(h.passwordPolicy(), req.Password); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}

	// 设置默认角色；角色必须是白名单内的合法值
	if req.Role == "" {
		req.Role = "viewer"
	}
	if !validRoles[req.Role] {
		Fail(c, http.StatusBadRequest, "角色不合法，仅支持 admin/operator/viewer")
		return
	}

	// 哈希密码
	passwordHash, err := middleware.HashPassword(req.Password)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "密码处理失败", err)
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
		ErrorWithMessage(c, http.StatusInternalServerError, "创建用户失败", err)
		return
	}

	Created(c, "创建成功", user)
}

// UpdateUser 更新用户（PUT /api/users/:id，仅 admin）。
//
// 请求体全部用指针字段：nil 表示「本次未提交该字段」，从而区分「不改」与
// 「改成零值」（例如 is_active=false、real_name=""）。只把出现的字段放进
// updates map，未提交的列保持原值。角色仍必须过白名单。
func (h *UserHandler) UpdateUser(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}

	var user model.User
	if err := h.DB.First(&user, id).Error; err != nil {
		lookupError(c, err, "用户不存在")
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
		// 角色必须是白名单内的合法值
		if !validRoles[*req.Role] {
			Fail(c, http.StatusBadRequest, "角色不合法，仅支持 admin/operator/viewer")
			return
		}
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
		ErrorWithMessage(c, http.StatusInternalServerError, "更新用户失败", err)
		return
	}

	Created(c, "更新成功", nil)
}

// ChangeMyPassword 当前登录用户修改密码（PUT /api/users/me/password，所有角色可用）。
//
// 安全要点：旧密码只用于身份确认，不参与强度策略；新密码走系统策略；新旧相同
// 直接拒绝，避免「改了个寂寞」。改密成功只回提示，前端负责清理本地登录态并
// 引导重新登录（JWT 无状态，服务端不维护吊销列表）。
func (h *UserHandler) ChangeMyPassword(c *gin.Context) {
	uid, ok := c.Get("user_id")
	if !ok || uid == nil {
		Fail(c, http.StatusUnauthorized, "未登录")
		return
	}
	var me model.User
	if err := h.DB.First(&me, uid).Error; err != nil {
		lookupError(c, err, "用户不存在")
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.OldPassword == "" || req.NewPassword == "" {
		Fail(c, http.StatusBadRequest, "请填写旧密码和新密码")
		return
	}
	// 密码策略（v3 批次 D）：新密码按系统设置 password_min_length 校验（含复杂度要求），
	// 旧密码不参与策略（它只用于身份确认）。错误文案是固定中文，可直接回显。
	if err := setting.ValidatePassword(h.passwordPolicy(), req.NewPassword); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.OldPassword == req.NewPassword {
		Fail(c, http.StatusBadRequest, "新密码不能与旧密码相同")
		return
	}
	if !middleware.CheckPassword(req.OldPassword, me.PasswordHash) {
		Fail(c, http.StatusBadRequest, "旧密码不正确")
		return
	}
	hash, err := middleware.HashPassword(req.NewPassword)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "密码加密失败", err)
		return
	}
	if err := h.DB.Model(&me).Update("password_hash", hash).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "修改密码失败", err)
		return
	}
	Success(c, gin.H{"message": "密码修改成功，请重新登录"})
}

// DeleteUser 删除用户（DELETE /api/users/:id，仅 admin）。
//
// 两个约束：
//   - 禁止删除自己：避免管理员误操作后把自己锁在系统外（也防止最后一个 admin 自删）；
//   - 软删除：users 是资产表，记录保留供审计/恢复，GORM 自动加 deleted_at 条件。
func (h *UserHandler) DeleteUser(c *gin.Context) {
	// 路径参数主键必须经 paramID 解析（防 GORM 内联条件注入），禁止直传 c.Param
	id, ok := paramID(c, "id")
	if !ok {
		return
	}

	// 不能删除自己
	currentUserID, _ := c.Get("user_id")
	var userID uint
	if v, ok := currentUserID.(uint); ok {
		userID = v
	}

	if userID == id {
		Fail(c, http.StatusBadRequest, "不能删除自己")
		return
	}

	var user model.User
	if err := h.DB.First(&user, id).Error; err != nil {
		lookupError(c, err, "用户不存在")
		return
	}

	if err := h.DB.Delete(&user).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "删除用户失败", err)
		return
	}

	Created(c, "删除成功", nil)
}
