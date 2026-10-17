// user_group.go：用户组与「组 → VM」批量授权。
//
// 教学场景下按组授权比逐人授权更易管理：组是成员的集合，VMGroupGrant 把一台 VM
// 授权给整组，组内成员因此获得该 VM 的可见性。组 CRUD / 成员维护全部要求 admin；
// 组授权端点挂在 VM 路由下，同样由 admin 中间件保护。
//
// 数据模型（model/user_group.go）：
//   - UserGroupMember：组与用户 n:m，(group_id,user_id) 唯一；
//   - VMGroupGrant：(group_id,vm_id) 唯一，ExpiresAt=nil 表示长期有效。
// 本文件不含外键级联，删除组时需显式清理成员与授权（见 DeleteGroup）。

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
//
// 只持有 DB：组操作全是平台元数据 CRUD，不触碰 libvirt；组授权对 VM 的鉴权
// 由查询侧（可见性过滤）消费，本 handler 只负责写授权关系。
type UserGroupHandler struct {
	DB *gorm.DB
}

// ListGroups 用户组列表（含成员数与组授权数）。GET /api/user-groups（admin）。
//
// 实现取舍：成员关系一次性全量拉出后在内存聚合，避免「每组一条查询」的 N+1；
// 组数量级很小，全量扫描成本可忽略。成员关系查询失败时静默降级为空统计——
// 列表主功能不应因为附加统计失败而整体不可用。授权数按组各查一次 Count，
// 同样受组数量级约束。
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
		MemberCount int64  `json:"member_count"`
		GrantCount  int64  `json:"grant_count"`
		MemberIDs   []uint `json:"member_ids"`
	}
	items := make([]groupItem, 0, len(groups))
	for _, g := range groups {
		item := groupItem{UserGroup: g, MemberCount: memberCount[g.ID], MemberIDs: memberIDs[g.ID]}
		h.DB.Model(&model.VMGroupGrant{}).Where("group_id = ?", g.ID).Count(&item.GrantCount)
		items = append(items, item)
	}
	Success(c, gin.H{"total": len(items), "items": items})
}

// CreateGroup 新建用户组。POST /api/user-groups（admin）body: {name*, description?}
//
// 先 TrimSpace 再判空：只有空白的组名不应落库。重名先查一次返回友好的 409；
// 并发下仍可能同时通过检查，最终由 user_groups.name 唯一索引兜底（此时返回 500）。
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

// UpdateGroup 编辑用户组（仅改描述）。PUT /api/user-groups/:id（admin）。
//
// 组名不可改（唯一索引 + 授权关系都以 ID 关联，改名没有技术障碍，但当前产品
// 语义只开放描述编辑）。Description 用指针区分「未提交」与「清空为空串」；
// RowsAffected==0 表示 ID 不存在（值未变化时 GORM 也可能返回 0，故仅用于 404 判定）。
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

// DeleteGroup 删除用户组（级联清空成员与组授权——组没了授权随组消亡）。
// DELETE /api/user-groups/:id（admin）。
//
// 模型层没有数据库外键，级联必须在这里显式做，否则会留下指向已删组的悬挂
// 成员/授权记录（鉴权查询会关联到不存在的组）。顺序是先删组再清关联：
// 组删除失败时直接返回，不会误清关联。
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

// SetMembers 全量替换组成员。POST /api/user-groups/:id/members（admin）
// body: {user_ids: []}（前端用成员多选框一次提交，整体替换比增量增删简单且幂等）。
//
// 流程：确认组存在 → 校验所有用户真实存在 → 事务内「先清空再逐条插入」。
// 事务保证不会出现「清空了但插入失败」的半成品状态；即使绕过前置校验，
// 重复 user_id 也会因 (group_id,user_id) 唯一索引报错并整体回滚。
// Count 与 len 的比对同时挡住了不存在的 ID 和重复 ID（重复时 count < len）。
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
//
// findVM 内部已完成路径 ID 的 paramID 解析与存在性校验，这里直接拿 vm.ID 使用。
// 组名单独一次 IN 查询后回填，避免给每条授权都关联一次（同样是 N+1 规避）；
// 已删除的组查不到名字时 GroupName 为空串，前端按「组已删除」展示即可。
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
//
// 语义是 upsert：先按唯一键查，命中则更新 expires_at/granted_by，未命中才 Create。
// 这样前端重复提交同一授权不会报唯一键冲突。过期时间必须晚于当前时刻；nil 表示
// 长期有效（鉴权查询时判定，过期记录无需后台清扫）。granted_by 记录操作管理员，
// 便于审计「谁把机器给了这个组」。
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
//
// 删除条件同时带 id 和 vm_id：只拿到 gid 时也必须在当前 VM 作用域内命中，
// 防止用 A 机的 URL 删掉 B 机的授权记录（越权/IDOR 防护）。
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
