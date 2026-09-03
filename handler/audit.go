package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// AuditHandler 审计日志处理器
type AuditHandler struct {
	DB *gorm.DB
}

// NewAuditHandler 创建审计日志处理器
func NewAuditHandler(db *gorm.DB) *AuditHandler {
	return &AuditHandler{DB: db}
}

// ListAuditLogs 查询审计日志（支持多条件筛选与分页）
func (h *AuditHandler) ListAuditLogs(c *gin.Context) {
	query := h.DB.Model(&model.AuditLog{})

	// 按操作类型筛选
	if action := c.Query("action"); action != "" {
		query = query.Where("action = ?", action)
	}
	// 按对象类型筛选
	if objectType := c.Query("object_type"); objectType != "" {
		query = query.Where("object_type = ?", objectType)
	}
	// 按用户名筛选
	if username := c.Query("username"); username != "" {
		query = query.Where("username LIKE ?", "%"+username+"%")
	}
	// 按操作状态筛选
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}
	// 按时间范围筛选
	if start := c.Query("start"); start != "" {
		if t, err := time.Parse("2006-01-02", start); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if end := c.Query("end"); end != "" {
		if t, err := time.Parse("2006-01-02", end); err == nil {
			// 包含当天，加一天
			query = query.Where("created_at < ?", t.Add(24*time.Hour))
		}
	}

	// 总数统计
	var total int64
	query.Count(&total)

	// 分页
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var logs []model.AuditLog
	if err := query.Order("created_at desc").Offset(offset).Limit(pageSize).Find(&logs).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "查询审计日志失败")
		return
	}

	Success(c, gin.H{
		"total":    total,
		"page":     page,
		"page_size": pageSize,
		"items":    logs,
	})
}

// GetAuditLog 获取单条审计日志详情
func (h *AuditHandler) GetAuditLog(c *gin.Context) {
	id := c.Param("id")

	var log model.AuditLog
	if err := h.DB.First(&log, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "审计日志不存在")
		return
	}

	Success(c, log)
}

// AuditActionSummary 操作类型分布统计（用于仪表盘/论文图表）
func (h *AuditHandler) AuditActionSummary(c *gin.Context) {
	type actionCount struct {
		Action string `json:"action"`
		Count  int64  `json:"count"`
	}

	var result []actionCount
	h.DB.Model(&model.AuditLog{}).
		Select("action, count(*) as count").
		Group("action").
		Order("count desc").
		Scan(&result)

	Success(c, result)
}
