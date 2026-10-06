package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// NotificationHandler 站内通知（告警到人）：当前登录人自己的通知列表 / 已读流转。
// 投递侧在 alert_webhook.go（告警转 firing 时按「admin 全体 + VM 被授权人」写入）。
type NotificationHandler struct {
	DB *gorm.DB
}

// NewNotificationHandler 创建站内通知处理器。
func NewNotificationHandler(db *gorm.DB) *NotificationHandler {
	return &NotificationHandler{DB: db}
}

// List GET /api/notifications?page=1&page_size=20&unread=1
// 只返回当前登录人自己的通知（user_id 过滤，越权无从谈起）；按时间倒序。
// unread 一并返回：顶栏铃铛红点与列表共用一次请求。
func (h *NotificationHandler) List(c *gin.Context) {
	uid := c.GetUint("user_id")
	q := h.DB.Model(&model.Notification{}).Where("user_id = ?", uid)
	if c.Query("unread") == "1" {
		q = q.Where("`read` = ?", false)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	page, pageSize := parsePageQuery(c, 20, 100)
	var items []model.Notification
	if err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if items == nil {
		items = []model.Notification{}
	}
	var unread int64
	if err := h.DB.Model(&model.Notification{}).Where("user_id = ? AND `read` = ?", uid, false).Count(&unread).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize, "unread": unread})
}

// MarkRead PUT /api/notifications/:id/read —— 单条标记已读（仅自己的通知可操作）。
func (h *NotificationHandler) MarkRead(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	uid := c.GetUint("user_id")
	res := h.DB.Model(&model.Notification{}).Where("id = ? AND user_id = ?", id, uid).Update("`read`", true)
	if res.Error != nil {
		ErrorResponse(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, "通知不存在")
		return
	}
	Success(c, gin.H{"ok": true})
}

// MarkAllRead PUT /api/notifications/read-all —— 全部已读（铃铛 popover「全部已读」入口）。
func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	uid := c.GetUint("user_id")
	if err := h.DB.Model(&model.Notification{}).Where("user_id = ? AND `read` = ?", uid, false).Update("`read`", true).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"ok": true})
}
