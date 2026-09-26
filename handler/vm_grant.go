package handler

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// vmGrantActive 授权是否有效（nil=长期有效；过期即失效）。纯函数，供单测。
func vmGrantActive(expiresAt *time.Time, now time.Time) bool {
	return expiresAt == nil || expiresAt.After(now)
}

// vmVisible 授权决定可见性（借鉴堡垒机 4A）：admin 全量；其余角色需持有该 VM 的有效授权。
// 未授权返回 false，调用方统一按「虚拟机不存在」响应——对齐堡垒机「查无此项」语义，
// 不向未授权用户泄露资产存在性（比 403 更严：连「有这台机器但不给你看」都不暴露）。
func vmVisible(c *gin.Context, db *gorm.DB, vmID uint) bool {
	if roleIsAdmin(c) {
		return true
	}
	uid, _ := taskUserFromContext(c)
	if uid == nil {
		return false
	}
	var cnt int64
	if err := db.Model(&model.VMGrant{}).
		Where("user_id = ? AND vm_id = ?", *uid, vmID).
		Where("expires_at IS NULL OR expires_at > ?", time.Now()).
		Count(&cnt).Error; err != nil {
		// 查询失败按未授权处理（fail-closed），但必须留痕否则用户"全部 VM 消失"无从排查
		log.Printf("[grant] 授权查询失败（按未授权处理）vm=%d user=%d err=%v", vmID, *uid, err)
	}
	if cnt > 0 {
		return true
	}
	// 组授权并集：用户所属组的有效组授权同样可见（教学场景「全班开一批实验机」）
	vmIDs := groupGrantedVMIDs(db, *uid)
	return vmIDs[vmID]
}

// grantedVMIDs 非 admin 用户的全部有效授权 VM 集合（列表过滤用）：
// 直接授权 ∪ 组授权（用户所属组的有效 vm_group_grants）。
func grantedVMIDs(db *gorm.DB, userID uint) map[uint]bool {
	var ids []uint
	if err := db.Model(&model.VMGrant{}).
		Where("user_id = ?", userID).
		Where("expires_at IS NULL OR expires_at > ?", time.Now()).
		Pluck("vm_id", &ids).Error; err != nil {
		log.Printf("[grant] 授权集合查询失败（按空集处理）user=%d err=%v", userID, err)
	}
	set := make(map[uint]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	for id := range groupGrantedVMIDs(db, userID) {
		set[id] = true
	}
	return set
}

// groupGrantedVMIDs 用户经「所属组 → 组授权」间接持有的有效 VM 集合。
// 过期判定与直接授权逐字一致（expires_at IS NULL OR > now）。
func groupGrantedVMIDs(db *gorm.DB, userID uint) map[uint]bool {
	set := map[uint]bool{}
	var groupIDs []uint
	if err := db.Model(&model.UserGroupMember{}).
		Where("user_id = ?", userID).
		Pluck("group_id", &groupIDs).Error; err != nil {
		log.Printf("[grant] 组成员查询失败（按空集处理）user=%d err=%v", userID, err)
		return set
	}
	if len(groupIDs) == 0 {
		return set
	}
	var vmIDs []uint
	if err := db.Model(&model.VMGroupGrant{}).
		Where("group_id IN ?", groupIDs).
		Where("expires_at IS NULL OR expires_at > ?", time.Now()).
		Pluck("vm_id", &vmIDs).Error; err != nil {
		log.Printf("[grant] 组授权查询失败（按空集处理）user=%d err=%v", userID, err)
		return set
	}
	for _, id := range vmIDs {
		set[id] = true
	}
	return set
}

// roleIsAdmin 判定当前请求角色是否 admin。
func roleIsAdmin(c *gin.Context) bool {
	role, _ := c.Get("role")
	s, _ := role.(string)
	return s == "admin"
}

// requireAdminRole 授权管理等敏感操作的角色闸（路由前缀在 /api/vms 下，
// OperatorMiddleware 对 operator 放行该前缀的写操作，此处二次收口为仅 admin）。
func requireAdminRole(c *gin.Context) bool {
	if roleIsAdmin(c) {
		return true
	}
	Fail(c, http.StatusForbidden, "授权管理仅管理员可用")
	return false
}

// ListVMGrants 查看某 VM 的授权列表（admin）。GET /api/vms/:id/grants
func (h *VMHandler) ListVMGrants(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	var grants []model.VMGrant
	if err := h.DB.Where("vm_id = ?", vm.ID).Order("created_at desc").Find(&grants).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 关联用户名（一次查询）
	userIDs := make([]uint, 0, len(grants))
	for _, g := range grants {
		userIDs = append(userIDs, g.UserID)
	}
	names := map[uint]string{}
	if len(userIDs) > 0 {
		var users []model.User
		h.DB.Where("id IN ?", userIDs).Select("id", "username").Find(&users)
		for _, u := range users {
			names[u.ID] = u.Username
		}
	}
	type grantItem struct {
		model.VMGrant
		Username string `json:"username"`
	}
	items := make([]grantItem, 0, len(grants))
	for _, g := range grants {
		items = append(items, grantItem{VMGrant: g, Username: names[g.UserID]})
	}
	Success(c, gin.H{"total": len(items), "items": items})
}

// GrantVM 把 VM 授权给某用户（admin）。POST /api/vms/:id/grants
// body: {user_id*, expires_at?（RFC3339，缺省长期有效）}；(user_id, vm_id) 已存在时更新有效期。
func (h *VMHandler) GrantVM(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	var req struct {
		UserID    uint       `json:"user_id" binding:"required"`
		ExpiresAt *time.Time `json:"expires_at"` // RFC3339；省略 = 长期有效
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误（需提供 user_id）", err)
		return
	}
	var user model.User
	if err := h.DB.First(&user, req.UserID).Error; err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "用户不存在", err)
		return
	}
	if req.ExpiresAt != nil && req.ExpiresAt.Before(time.Now()) {
		Fail(c, http.StatusBadRequest, "有效期不能早于当前时间")
		return
	}

	adminID, _ := taskUserFromContext(c)
	grantedBy := uint(0)
	if adminID != nil {
		grantedBy = *adminID
	}

	var grant model.VMGrant
	err := h.DB.Where("user_id = ? AND vm_id = ?", req.UserID, vm.ID).First(&grant).Error
	if err == nil {
		// 重复授权 = 调整有效期
		if err := h.DB.Model(&grant).Updates(map[string]interface{}{"expires_at": req.ExpiresAt, "granted_by": grantedBy}).Error; err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		Success(c, gin.H{"grant": grant, "message": "已更新授权有效期"})
		return
	}
	grant = model.VMGrant{UserID: req.UserID, VMID: vm.ID, ExpiresAt: req.ExpiresAt, GrantedBy: grantedBy}
	if err := h.DB.Create(&grant).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"grant": grant, "message": "已授权给 " + user.Username})
}

// RevokeVMGrant 收回授权（admin）。DELETE /api/vms/:id/grants/:gid
func (h *VMHandler) RevokeVMGrant(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	gid, ok := paramID(c, "gid")
	if !ok {
		return
	}
	res := h.DB.Where("id = ? AND vm_id = ?", gid, vm.ID).Delete(&model.VMGrant{})
	if res.Error != nil {
		ErrorResponse(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, "授权记录不存在")
		return
	}
	Success(c, gin.H{"message": "已收回授权"})
}
