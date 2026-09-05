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

// ListSessions 会话列表（进行中优先，其次按开始时间倒序，默认 100 条）。
func (h *SessionHandler) ListSessions(c *gin.Context) {
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	var items []model.ConsoleSession
	query := h.DB.Order("CASE WHEN status='active' THEN 0 ELSE 1 END, started_at DESC").Limit(limit)
	if status := c.Query("status"); status == "active" || status == "closed" {
		query = query.Where("status = ?", status)
	}
	if err := query.Find(&items).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询会话失败", err)
		return
	}
	if items == nil {
		items = []model.ConsoleSession{}
	}
	Success(c, gin.H{"total": len(items), "items": items})
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
