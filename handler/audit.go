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
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"items":     logs,
	})
}

// GetAuditLog 获取单条审计日志详情
func (h *AuditHandler) GetAuditLog(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}

	var log model.AuditLog
	if err := h.DB.First(&log, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "审计日志不存在")
		return
	}

	Success(c, log)
}

// ActionLabels 审计操作类型 → 中文文案映射（供审计页筛选/展示、仪表盘统计标签使用）。
// 与 middleware.AuditMiddleware 的 determineAction 产物保持一致。
var ActionLabels = map[string]string{
	"login": "登录", "logout": "登出",

	"create_vm": "创建虚拟机", "delete_vm": "删除虚拟机", "start_vm": "开机",
	"stop_vm": "关机", "restart_vm": "重启", "import_vm": "导入虚拟机",
	"pause_vm": "暂停虚拟机", "resume_vm": "恢复虚拟机", "clone_vm": "克隆虚拟机",

	"attach_disk": "挂载磁盘", "detach_disk": "移除磁盘",
	"attach_nic": "添加网卡", "detach_nic": "移除网卡",
	"create_snapshot": "创建快照", "delete_snapshot": "删除快照", "revert_snapshot": "回滚快照",

	"update_vm_spec": "更新虚拟机配置", "update_vm_xml": "更新虚拟机XML", "update_vm": "更新虚拟机",
	"set_vcpu": "调整CPU核数", "set_memory": "调整内存",
	"set_autostart": "设置开机自启", "set_boot": "设置引导顺序",

	"create_host": "添加宿主机", "update_host": "更新宿主机", "delete_host": "删除宿主机",

	"upload_image": "上传镜像", "delete_image": "删除镜像",
	"set_image_template": "设置镜像模板", "clone_image": "镜像创建虚拟机",

	"create_network": "创建网络", "update_network": "更新网络", "delete_network": "删除网络",

	"create_volume": "创建存储卷", "delete_volume": "删除存储卷",

	"access": "访问",
}

// ListAuditActions 返回操作类型 → 中文文案映射（前端下拉/标签展示）。
func (h *AuditHandler) ListAuditActions(c *gin.Context) {
	Success(c, ActionLabels)
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
