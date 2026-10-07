// hostgroup.go：运维主机组 CRUD（AU1）。组是批量执行目标的快捷集合，
// 成员为 VM ID 列表；执行侧（adhoc/playbook/定时任务）按组一键选中。
//
// 路由挂 ansible 组（operator+），与快速执行同一权限面。
package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// HostGroupHandler 主机组处理器。
type HostGroupHandler struct {
	DB *gorm.DB
}

// NewHostGroupHandler 创建主机组处理器。
func NewHostGroupHandler(db *gorm.DB) *HostGroupHandler {
	return &HostGroupHandler{DB: db}
}

// hostGroupReq 创建/更新请求体。
type hostGroupReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	VMIDs       *[]uint `json:"vm_ids"`
}

// trimSpace 去首尾空白（组名/描述统一收口）。
func trimSpace(s string) string { return strings.TrimSpace(s) }

// parseGroupVMIDs 解析组成员 JSON；坏数据按空处理（组是便捷入口，不因脏数据 500）。
func parseGroupVMIDs(s string) []uint {
	ids := []uint{}
	if s == "" {
		return ids
	}
	_ = json.Unmarshal([]byte(s), &ids)
	return ids
}

// List GET /api/ansible/host-groups：全部主机组（成员 ID 已展开为数组）。
func (h *HostGroupHandler) List(c *gin.Context) {
	var groups []model.HostGroup
	if err := h.DB.Order("id").Find(&groups).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]gin.H, 0, len(groups))
	for _, g := range groups {
		items = append(items, gin.H{
			"id": g.ID, "name": g.Name, "description": g.Description,
			"vm_ids": parseGroupVMIDs(g.VMIDs), "created_at": g.CreatedAt,
		})
	}
	Success(c, gin.H{"total": len(items), "items": items})
}

// ensureGroupVMs 校验成员 VM 全部存在（软删排除），返回 false 表示响应已写好。
func (h *HostGroupHandler) ensureGroupVMs(c *gin.Context, ids []uint) bool {
	if len(ids) == 0 {
		return true
	}
	if len(ids) > 100 {
		Fail(c, http.StatusBadRequest, "单组最多 100 台成员")
		return false
	}
	var cnt int64
	if err := h.DB.Model(&model.VM{}).Where("id IN ?", ids).Count(&cnt).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return false
	}
	if cnt != int64(len(uniqueUint(ids))) {
		Fail(c, http.StatusBadRequest, "成员包含不存在或已删除的虚拟机")
		return false
	}
	return true
}

func uniqueUint(ids []uint) []uint {
	seen := map[uint]bool{}
	out := []uint{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Create POST /api/ansible/host-groups。
func (h *HostGroupHandler) Create(c *gin.Context) {
	var req hostGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	g := model.HostGroup{}
	if req.Name != nil {
		g.Name = trimSpace(*req.Name)
	}
	if req.Description != nil {
		g.Description = trimSpace(*req.Description)
	}
	if g.Name == "" || utf8.RuneCountInString(g.Name) > 50 {
		Fail(c, http.StatusBadRequest, "组名不能为空且不超过 50 字")
		return
	}
	var ids []uint
	if req.VMIDs != nil {
		ids = uniqueUint(*req.VMIDs)
	}
	if !h.ensureGroupVMs(c, ids) {
		return
	}
	// 同名互斥给明确文案（uniqueIndex 兜底）
	var dup int64
	h.DB.Model(&model.HostGroup{}).Where("name = ?", g.Name).Count(&dup)
	if dup > 0 {
		Fail(c, http.StatusBadRequest, "组名「"+g.Name+"」已存在")
		return
	}
	raw, _ := json.Marshal(ids)
	g.VMIDs = string(raw)
	if err := h.DB.Select("Name", "Description", "VMIDs").Create(&g).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Created(c, "主机组已创建", gin.H{"id": g.ID, "name": g.Name, "vm_ids": ids})
}

// Update PUT /api/ansible/host-groups/:id。
func (h *HostGroupHandler) Update(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var g model.HostGroup
	if err := h.DB.First(&g, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "主机组不存在", err)
		return
	}
	var req hostGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Name != nil {
		name := trimSpace(*req.Name)
		if name == "" || utf8.RuneCountInString(name) > 50 {
			Fail(c, http.StatusBadRequest, "组名不能为空且不超过 50 字")
			return
		}
		var dup int64
		h.DB.Model(&model.HostGroup{}).Where("name = ? AND id <> ?", name, id).Count(&dup)
		if dup > 0 {
			Fail(c, http.StatusBadRequest, "组名「"+name+"」已存在")
			return
		}
		g.Name = name
	}
	if req.Description != nil {
		g.Description = trimSpace(*req.Description)
	}
	var ids []uint
	if req.VMIDs != nil {
		ids = uniqueUint(*req.VMIDs)
		if !h.ensureGroupVMs(c, ids) {
			return
		}
		raw, _ := json.Marshal(ids)
		g.VMIDs = string(raw)
	}
	if err := h.DB.Save(&g).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"id": g.ID, "name": g.Name, "vm_ids": parseGroupVMIDs(g.VMIDs)})
}

// Delete DELETE /api/ansible/host-groups/:id。
func (h *HostGroupHandler) Delete(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	res := h.DB.Delete(&model.HostGroup{}, id)
	if res.Error != nil {
		ErrorResponse(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, "主机组不存在")
		return
	}
	Success(c, gin.H{"message": "主机组已删除"})
}
