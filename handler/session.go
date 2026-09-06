package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/console"
	"gorm.io/gorm"
)

// SessionHandler 控制台会话处理器（谁连了哪台 VM、强制断开）。
type SessionHandler struct {
	DB       *gorm.DB
	Sessions *console.Registry
}

// NewSessionHandler 创建会话处理器。
func NewSessionHandler(db *gorm.DB, sessions *console.Registry) *SessionHandler {
	return &SessionHandler{DB: db, Sessions: sessions}
}

// ListSessions 会话列表（进行中优先，其次按开始时间倒序）。
// 支持 status/type 精确过滤、vm_name/username 模糊过滤、page/page_size 真分页（total 为真实总数）。
func (h *SessionHandler) ListSessions(c *gin.Context) {
	page := 1
	if s := c.Query("page"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			page = n
		}
	}
	pageSize := 20
	if s := c.Query("page_size"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 500 {
			pageSize = n
		}
	}

	query := h.DB.Model(&model.ConsoleSession{})
	if status := c.Query("status"); status == "active" || status == "closed" {
		query = query.Where("status = ?", status)
	}
	if t := c.Query("type"); t == "vnc" || t == "ssh" || t == "serial" {
		query = query.Where("type = ?", t)
	}
	if q := c.Query("vm_name"); q != "" {
		query = query.Where("vm_name LIKE ?", "%"+q+"%")
	}
	if q := c.Query("username"); q != "" {
		query = query.Where("username LIKE ?", "%"+q+"%")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "统计会话总数失败", err)
		return
	}

	var items []model.ConsoleSession
	if err := query.
		Order("CASE WHEN status='active' THEN 0 ELSE 1 END, started_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&items).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询会话失败", err)
		return
	}
	if items == nil {
		items = []model.ConsoleSession{}
	}
	Success(c, gin.H{"total": total, "items": items})
}

// DisconnectSession 强制断开会话。
// ssh/serial 关闭服务端 WS（浏览器端随即掉线）；VNC 中转连接无法切断，返回明确提示。
func (h *SessionHandler) DisconnectSession(c *gin.Context) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	var sess model.ConsoleSession
	if err := h.DB.First(&sess, uint(id64)).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "会话不存在", err)
		return
	}
	if sess.Status != "active" {
		Fail(c, http.StatusBadRequest, "会话已结束")
		return
	}
	if h.Sessions == nil {
		Fail(c, http.StatusInternalServerError, "会话服务不可用")
		return
	}
	if err := h.Sessions.Disconnect(sess.ID); err != nil {
		if err == console.ErrNoLiveConn {
			Fail(c, http.StatusBadRequest, "VNC 为中转连接，无法强制断开（关闭浏览器页签即可结束）")
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"message": "已断开会话"})
}
