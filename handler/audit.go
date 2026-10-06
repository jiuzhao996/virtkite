package handler

import (
	"encoding/csv"
	"log"
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
	// 筛选条件与 CSV 导出共用 applyAuditFilters（同一口径，防两处漂移）
	query := h.applyAuditFilters(c)

	// 总数统计（失败按 500 返回，不带着 total=0 继续查——两段查询同一口径）
	var total int64
	if err := query.Count(&total).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 分页
	page, pageSize := parsePageQuery(c, 20, 100)
	offset := (page - 1) * pageSize

	var logs []model.AuditLog
	if err := query.Order("created_at desc").Offset(offset).Limit(pageSize).Find(&logs).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "查询审计日志失败")
		return
	}
	if logs == nil {
		logs = []model.AuditLog{}
	}

	Success(c, gin.H{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"items":     logs,
	})
}

// applyAuditFilters 审计查询的共用筛选器（列表与 CSV 导出同一套口径，防两处漂移）。
func (h *AuditHandler) applyAuditFilters(c *gin.Context) *gorm.DB {
	query := h.DB.Model(&model.AuditLog{})
	if action := c.Query("action"); action != "" {
		query = query.Where("action = ?", action)
	}
	if objectType := c.Query("object_type"); objectType != "" {
		query = query.Where("object_type = ?", objectType)
	}
	if username := c.Query("username"); username != "" {
		query = query.Where("username LIKE ?", "%"+username+"%")
	}
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}
	if start := c.Query("start"); start != "" {
		if t, err := time.Parse("2006-01-02", start); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if end := c.Query("end"); end != "" {
		if t, err := time.Parse("2006-01-02", end); err == nil {
			query = query.Where("created_at < ?", t.Add(24*time.Hour))
		}
	}
	return query
}

// ExportAuditCSV GET /api/audit/export（admin）——按当前筛选流式导出 CSV。
// 后端导出替代前端拼接（S1-3）：前端曾按 page_size=500 循环拉全量再拼 CSV，
// 万条级审计会卡浏览器；此处 FindInBatches 每千条 flush 一次，内存占用恒定。
// Excel 兼容：UTF-8 BOM 头；encoding/csv 自带引号转义（含逗号/换行的单元格安全）。
func (h *AuditHandler) ExportAuditCSV(c *gin.Context) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=audit-logs.csv")

	_, _ = c.Writer.WriteString("\ufeff") // BOM：Excel 双击打开不乱码
	w := csv.NewWriter(c.Writer)

	_ = w.Write([]string{"ID", "时间", "用户", "操作", "操作文案", "对象类型", "对象ID", "来源IP", "状态", "详情"})

	query := h.applyAuditFilters(c).Order("created_at desc")
	const batch = 1000
	var rows []model.AuditLog
	err := query.FindInBatches(&rows, batch, func(_ *gorm.DB, _ int) error {
		out := make([][]string, 0, len(rows))
		for _, l := range rows {
			objID := ""
			if l.ObjectID != nil {
				objID = strconv.FormatUint(uint64(*l.ObjectID), 10)
			}
			label, hasLabel := ActionLabels[l.Action]
			if !hasLabel {
				label = l.Action
			}
			out = append(out, []string{
				strconv.FormatUint(uint64(l.ID), 10),
				l.CreatedAt.Format("2006-01-02 15:04:05"),
				l.Username,
				l.Action,
				label,
				l.ObjectType,
				objID,
				l.SourceIP,
				l.Status,
				l.Detail,
			})
		}
		if err := w.WriteAll(out); err != nil {
			return err
		}
		w.Flush()
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}).Error
	if err != nil {
		// 头已发出无法改状态码：写一行错误说明收尾（CSV 前部数据仍有效）
		_ = w.Write([]string{"导出中断", err.Error()})
		w.Flush()
		log.Printf("[audit] CSV 导出中断: %v", err)
		return
	}
	w.Flush()
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

	"jumpd.cmd": "跳板执行命令",
	"jumpd.cmd_blocked": "跳板拦截危险命令",

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
	if err := h.DB.Model(&model.AuditLog{}).
		Select("action, count(*) as count").
		Group("action").
		Order("count desc").
		Scan(&result).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if result == nil {
		result = []actionCount{}
	}

	Success(c, result)
}
