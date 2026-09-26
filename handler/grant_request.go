package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// GrantRequestHandler 资产授权申请/审批（学生自助申请 → 教师审批 → 限时授权）。
// 批准的唯一副作用是写 vm_grants（带 expires_at），可见性判定完全复用既有链路。
type GrantRequestHandler struct {
	DB *gorm.DB
}

// ApplyForAsset 学生申请资产授权。POST /api/vms/:id/grant-request（operator+，挂 vms 前缀
// 以通过 OperatorMiddleware；viewer 无终端能力，申请了也用不上，middleware 层即拒）。
// body: {reason*, hours?(默认 2，上限 720)}
func (h *VMHandler) ApplyForAsset(c *gin.Context) {
	// ⚠️ 不能走 findVM（其可见性检查会 404）——申请者的定义就是「尚未持有授权」，
	// 这里只校验 VM 存在，不校验可见性
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	uid, _ := taskUserFromContext(c)
	if uid == nil {
		Fail(c, http.StatusUnauthorized, "未登录")
		return
	}
	var req struct {
		Reason string `json:"reason" binding:"required"`
		Hours  int    `json:"hours"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "请填写申请理由", err)
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if len(req.Reason) < 5 {
		Fail(c, http.StatusBadRequest, "申请理由太短（至少 5 个字），请写明用途")
		return
	}
	if req.Hours <= 0 {
		req.Hours = 2
	}
	if req.Hours > 720 {
		Fail(c, http.StatusBadRequest, "单次申请时长不能超过 720 小时（30 天）")
		return
	}

	// 已持有有效授权 → 不必申请
	var granted int64
	h.DB.Model(&model.VMGrant{}).
		Where("user_id = ? AND vm_id = ? AND (expires_at IS NULL OR expires_at > ?)", *uid, vm.ID, time.Now()).
		Count(&granted)
	if granted > 0 {
		Fail(c, http.StatusBadRequest, "你已持有该虚拟机的有效授权，无需申请")
		return
	}

	// 待审批去重：同一台机器 pending 中只允许一张
	var pending int64
	h.DB.Model(&model.GrantRequest{}).
		Where("user_id = ? AND vm_id = ? AND status = ?", *uid, vm.ID, model.GrantRequestPending).
		Count(&pending)
	if pending > 0 {
		Fail(c, http.StatusConflict, "该虚拟机已有待审批的申请，请耐心等待教师处理")
		return
	}

	var u model.User
	_ = h.DB.Select("username").First(&u, *uid).Error
	gr := model.GrantRequest{
		UserID: *uid, Username: u.Username,
		VMID: vm.ID, VMName: vm.Name,
		Reason: req.Reason, Hours: req.Hours,
		Status: model.GrantRequestPending,
	}
	if err := h.DB.Create(&gr).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"request": gr, "message": "申请已提交，等待教师审批"})
}

// ApplyCatalog 申请目录：全部运行中 VM 的最小信息（id/name/status，不含 IP）。
// GET /api/vms/apply-catalog（operator+）——非 admin 用户看不到未授权资产明细，
// 但申请入口需要知道有哪些机器，故仅暴露无敏感字段的花名册。
func (h *VMHandler) ApplyCatalog(c *gin.Context) {
	var vms []model.VM
	if err := h.DB.Where("status = ?", model.VMStatusRunning).
		Select("id", "name", "status", "description").
		Order("name").Find(&vms).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	// 显式最小 DTO：花名册只含名称/状态/描述，防模型整序列化带出 uuid/ip 等字段形态
	items := make([]gin.H, 0, len(vms))
	for _, v := range vms {
		items = append(items, gin.H{"id": v.ID, "name": v.Name, "status": v.Status, "description": v.Description})
	}
	Success(c, gin.H{"total": len(items), "items": items})
}

// ListMine 我的申请记录。GET /api/grant-requests/mine
func (h *GrantRequestHandler) ListMine(c *gin.Context) {
	uid, _ := taskUserFromContext(c)
	if uid == nil {
		Fail(c, http.StatusUnauthorized, "未登录")
		return
	}
	var reqs []model.GrantRequest
	if err := h.DB.Where("user_id = ?", *uid).Order("created_at desc").Limit(100).Find(&reqs).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"total": len(reqs), "items": reqs})
}

// ListRequests 待审批/全部申请队列（admin）。GET /api/grant-requests?status=pending
func (h *GrantRequestHandler) ListRequests(c *gin.Context) {
	query := h.DB.Order("created_at desc").Limit(200)
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}
	var reqs []model.GrantRequest
	if err := query.Find(&reqs).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"total": len(reqs), "items": reqs})
}

// Approve 批准申请（admin）。POST /api/grant-requests/:id/approve  body: {hours?}
// 批准即写 vm_grants（expires = now + hours）；已有有效授权时到期取「更晚者」——
// 审批不应缩短用户已有的有效授权。
func (h *GrantRequestHandler) Approve(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Hours int `json:"hours"`
	}
	_ = c.ShouldBindJSON(&req)

	var gr model.GrantRequest
	if err := h.DB.First(&gr, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "申请不存在")
		return
	}
	if gr.Status != model.GrantRequestPending {
		Fail(c, http.StatusConflict, "该申请已处理（"+gr.Status+"）")
		return
	}
	hours := gr.Hours
	if req.Hours > 0 {
		if req.Hours > 720 {
			Fail(c, http.StatusBadRequest, "单次授权不能超过 720 小时（30 天）")
			return
		}
		hours = req.Hours
	}
	expires := time.Now().Add(time.Duration(hours) * time.Hour)

	adminID, _ := taskUserFromContext(c)
	now := time.Now()
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		// 已有有效授权 → 到期取更晚者（审批只延长不缩短）
		var existing model.VMGrant
		txErr := tx.Where("user_id = ? AND vm_id = ?", gr.UserID, gr.VMID).First(&existing).Error
		if txErr == nil {
			if existing.ExpiresAt != nil && existing.ExpiresAt.After(expires) {
				expires = *existing.ExpiresAt
			}
			if err := tx.Model(&existing).Updates(map[string]interface{}{"expires_at": &expires}).Error; err != nil {
				return err
			}
		} else {
			grant := model.VMGrant{UserID: gr.UserID, VMID: gr.VMID, ExpiresAt: &expires, GrantedBy: gr.UserID}
			if adminID != nil {
				grant.GrantedBy = *adminID
			}
			if err := tx.Create(&grant).Error; err != nil {
				return err
			}
		}
		return tx.Model(&gr).Updates(map[string]interface{}{
			"status": model.GrantRequestApproved, "decided_by": adminID,
			"decide_note": fmt.Sprintf("授权至 %s（%d 小时）", expires.Format("2006-01-02 15:04"), hours),
			"decided_at":  &now,
		}).Error
	})
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"message": fmt.Sprintf("已批准：授权至 %s", expires.Format("2006-01-02 15:04"))})
}

// Reject 驳回申请（admin）。POST /api/grant-requests/:id/reject  body: {note?}
func (h *GrantRequestHandler) Reject(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	var gr model.GrantRequest
	if err := h.DB.First(&gr, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "申请不存在")
		return
	}
	if gr.Status != model.GrantRequestPending {
		Fail(c, http.StatusConflict, "该申请已处理（"+gr.Status+"）")
		return
	}
	adminID, _ := taskUserFromContext(c)
	now := time.Now()
	if err := h.DB.Model(&gr).Updates(map[string]interface{}{
		"status": model.GrantRequestRejected, "decided_by": adminID,
		"decide_note": strings.TrimSpace(req.Note), "decided_at": &now,
	}).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"message": "已驳回"})
}
