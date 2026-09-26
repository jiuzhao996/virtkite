package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// UserGroupHandler 用户组管理（教学场景：按组批量授权，对标堡垒机的用户组）。
// 组本身与组成员管理、组授权均在 admin 组路由注册。
type UserGroupHandler struct {
	DB *gorm.DB
}

// ListGroups 用户组列表（含成员数与组授权数）。GET /api/user-groups
func (h *UserGroupHandler) ListGroups(c *gin.Context) {
	var groups []model.UserGroup
	if err := h.DB.Order("created_at desc").Find(&groups).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	// 一次拉全量成员关系，按组聚合出成员数与成员 ID 列表（成员弹窗回显用）
	type memberRow struct {
		GroupID uint
		UserID  uint
	}
	var memberRows []memberRow
	h.DB.Model(&model.UserGroupMember{}).Select("group_id", "user_id").Scan(&memberRows)
	memberCount := map[uint]int64{}
	memberIDs := map[uint][]uint{}
	for _, m := range memberRows {
		memberCount[m.GroupID]++
		memberIDs[m.GroupID] = append(memberIDs[m.GroupID], m.UserID)
	}
	type groupItem struct {
		model.UserGroup
		MemberCount int64   `json:"member_count"`
		GrantCount  int64   `json:"grant_count"`
		MemberIDs   []uint  `json:"member_ids"`
	}
	items := make([]groupItem, 0, len(groups))
	for _, g := range groups {
		item := groupItem{UserGroup: g, MemberCount: memberCount[g.ID], MemberIDs: memberIDs[g.ID]}
		h.DB.Model(&model.VMGroupGrant{}).Where("group_id = ?", g.ID).Count(&item.GrantCount)
		items = append(items, item)
	}
	Success(c, gin.H{"total": len(items), "items": items})
}

// CreateGroup 新建用户组。POST /api/user-groups  body: {name*, description?}
func (h *UserGroupHandler) CreateGroup(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误（需提供组名）", err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		Fail(c, http.StatusBadRequest, "组名不能为空")
		return
	}
	var cnt int64
	h.DB.Model(&model.UserGroup{}).Where("name = ?", req.Name).Count(&cnt)
	if cnt > 0 {
		Fail(c, http.StatusConflict, "组名已存在")
		return
	}
	g := model.UserGroup{Name: req.Name, Description: req.Description}
	if err := h.DB.Create(&g).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"group": g, "message": "用户组已创建"})
}

// UpdateGroup 编辑用户组（改描述）。PUT /api/user-groups/:id
func (h *UserGroupHandler) UpdateGroup(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Description *string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	updates := map[string]interface{}{}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if len(updates) == 0 {
		Fail(c, http.StatusBadRequest, "没有可更新的字段")
		return
	}
	res := h.DB.Model(&model.UserGroup{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		ErrorResponse(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, "用户组不存在")
		return
	}
	Success(c, gin.H{"message": "已更新"})
}

// DeleteGroup 删除用户组（级联清空成员与组授权——组没了授权随组消亡）。DELETE /api/user-groups/:id
func (h *UserGroupHandler) DeleteGroup(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	res := h.DB.Delete(&model.UserGroup{}, id)
	if res.Error != nil {
		ErrorResponse(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, "用户组不存在")
		return
	}
	h.DB.Where("group_id = ?", id).Delete(&model.UserGroupMember{})
	h.DB.Where("group_id = ?", id).Delete(&model.VMGroupGrant{})
	Success(c, gin.H{"message": "用户组已删除（成员与组授权已级联清理）"})
}

// SetMembers 全量替换组成员。POST /api/user-groups/:id/members  body: {user_ids: []}
// （前端用成员多选框一次提交，整体替换比增量增删简单且幂等）
func (h *UserGroupHandler) SetMembers(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var g model.UserGroup
	if err := h.DB.First(&g, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "用户组不存在")
		return
	}
	var req struct {
		UserIDs []uint `json:"user_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误（需提供 user_ids 数组）", err)
		return
	}
	// 校验用户真实存在（防悬挂成员）
	var cnt int64
	h.DB.Model(&model.User{}).Where("id IN ?", req.UserIDs).Count(&cnt)
	if int(cnt) != len(req.UserIDs) {
		Fail(c, http.StatusBadRequest, "包含不存在的用户")
		return
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", id).Delete(&model.UserGroupMember{}).Error; err != nil {
			return err
		}
		now := time.Now()
		for _, uid := range req.UserIDs {
			m := model.UserGroupMember{GroupID: id, UserID: uid, JoinedAt: now}
			if err := tx.Create(&m).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"message": "成员已更新", "count": len(req.UserIDs)})
}

// ListVMGroupGrants 某 VM 的组授权列表（admin）。GET /api/vms/:id/group-grants
func (h *VMHandler) ListVMGroupGrants(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	var grants []model.VMGroupGrant
	if err := h.DB.Where("vm_id = ?", vm.ID).Order("created_at desc").Find(&grants).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	// 组名一次关联（前端展示用）
	groupIDs := make([]uint, 0, len(grants))
	for _, g := range grants {
		groupIDs = append(groupIDs, g.GroupID)
	}
	names := map[uint]string{}
	if len(groupIDs) > 0 {
		var groups []model.UserGroup
		h.DB.Where("id IN ?", groupIDs).Select("id", "name").Find(&groups)
		for _, g := range groups {
			names[g.ID] = g.Name
		}
	}
	type grantItem struct {
		model.VMGroupGrant
		GroupName string `json:"group_name"`
	}
	items := make([]grantItem, 0, len(grants))
	for _, g := range grants {
		items = append(items, grantItem{VMGroupGrant: g, GroupName: names[g.GroupID]})
	}
	Success(c, gin.H{"total": len(items), "items": items})
}

// GrantVMToGroup 把 VM 授权给某组（admin）。POST /api/vms/:id/group-grants
// body: {group_id*, expires_at?（RFC3339，缺省长期）}；(group_id, vm_id) 已存在时更新有效期。
func (h *VMHandler) GrantVMToGroup(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	var req struct {
		GroupID   uint       `json:"group_id" binding:"required"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误（需提供 group_id）", err)
		return
	}
	var g model.UserGroup
	if err := h.DB.First(&g, req.GroupID).Error; err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "用户组不存在", err)
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
	var grant model.VMGroupGrant
	err := h.DB.Where("group_id = ? AND vm_id = ?", req.GroupID, vm.ID).First(&grant).Error
	if err == nil {
		if err := h.DB.Model(&grant).Updates(map[string]interface{}{"expires_at": req.ExpiresAt, "granted_by": grantedBy}).Error; err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		Success(c, gin.H{"grant": grant, "message": "已更新组授权有效期"})
		return
	}
	grant = model.VMGroupGrant{GroupID: req.GroupID, VMID: vm.ID, ExpiresAt: req.ExpiresAt, GrantedBy: grantedBy}
	if err := h.DB.Create(&grant).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"grant": grant, "message": "已授权给组「" + g.Name + "」"})
}

// RevokeVMGroupGrant 收回组授权（admin）。DELETE /api/vms/:id/group-grants/:gid
func (h *VMHandler) RevokeVMGroupGrant(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	gid, ok := paramID(c, "gid")
	if !ok {
		return
	}
	res := h.DB.Where("id = ? AND vm_id = ?", gid, vm.ID).Delete(&model.VMGroupGrant{})
	if res.Error != nil {
		ErrorResponse(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, "组授权记录不存在")
		return
	}
	Success(c, gin.H{"message": "已收回组授权"})
}
