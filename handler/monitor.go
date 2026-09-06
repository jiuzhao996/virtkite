package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/monitor"
	"gorm.io/gorm"
)

// MonitorHandler 监控处理器：代理 Alertmanager 当前告警列表，供前端"监控中心"页展示。
// 经后端转发而非浏览器直连，规避跨域，也避免暴露 Alertmanager 地址与端口。
// 另承载两块平台侧能力：file_sd 抓取目标预览、告警历史分页查询（webhook 入库数据）。
type MonitorHandler struct {
	DB              *gorm.DB
	Client          *http.Client
	AlertmanagerURL string
}

// NewMonitorHandler 创建监控处理器（alertmanagerURL 来自 config.ALERTMANAGER_URL）。
func NewMonitorHandler(db *gorm.DB, alertmanagerURL string) *MonitorHandler {
	return &MonitorHandler{
		DB:              db,
		Client:          &http.Client{Timeout: 5 * time.Second},
		AlertmanagerURL: alertmanagerURL,
	}
}

// ListAlerts GET /api/monitor/alerts → 转发 Alertmanager GET /api/v2/alerts。
// AM 的响应数组（labels/annotations/startsAt/status 等）原样透传给前端。
func (h *MonitorHandler) ListAlerts(c *gin.Context) {
	url := strings.TrimRight(h.AlertmanagerURL, "/") + "/api/v2/alerts"
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, url, nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "构造告警查询失败", err)
		return
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		// 典型场景：监控栈未启动（docker compose 未起 alertmanager 容器）
		ErrorResponse(c, http.StatusBadGateway, fmt.Errorf("Alertmanager 不可达(%s): %w", url, err))
		return
	}
	defer resp.Body.Close()

	// 上限 1MB：告警列表异常膨胀时不拖垮后端
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		LogError(c, fmt.Errorf("查询 Alertmanager 告警失败: status=%d err=%v", resp.StatusCode, err))
		Fail(c, http.StatusBadGateway, "查询告警失败（Alertmanager 响应异常）")
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

// PreviewFileSD GET /api/monitor/file-sd → 实时查库计算当前将生成的 file_sd JSON，
// 与 service/monitor.StartFileSDWriter 的落盘内容同源，便于前端/调试预览抓取目标。
func (h *MonitorHandler) PreviewFileSD(c *gin.Context) {
	var vms []model.VM
	// 与 writer.writeFileSDOnce 保持同一查询条件：running 且 IP 非空（软删除自动排除）
	if err := h.DB.Where("status = ? AND ip <> ?", model.VMStatusRunning, "").Find(&vms).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询虚拟机失败", err)
		return
	}
	Success(c, monitor.GenerateFileSD(vms))
}

// AlertHistory GET /api/monitor/alerts/history → 告警历史分页查询（webhook 入库数据）。
// 支持 status（firing/resolved）与 fingerprint 精确过滤，page/page_size 真分页（total 为真实总数），
// 按 UpdatedAt 倒序（最近一次状态流转优先）。labels/annotations 反序列化为对象返回。
func (h *MonitorHandler) AlertHistory(c *gin.Context) {
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

	query := h.DB.Model(&model.Alert{})
	if status := c.Query("status"); status == model.AlertStatusFiring || status == model.AlertStatusResolved {
		query = query.Where("status = ?", status)
	}
	if fp := c.Query("fingerprint"); fp != "" {
		query = query.Where("fingerprint = ?", fp)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "统计告警总数失败", err)
		return
	}

	var rows []model.Alert
	if err := query.
		Order("updated_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&rows).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询告警历史失败", err)
		return
	}

	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, alertHistoryItem(row))
	}
	Success(c, gin.H{"total": total, "items": items})
}

// alertHistoryItem 把告警行转换为响应条目：labels/annotations JSON 文本反序列化为对象，
// 反序列化失败（脏数据）降级为 null，不因单条告警拖垮整个列表。
func alertHistoryItem(a model.Alert) gin.H {
	return gin.H{
		"id":          a.ID,
		"fingerprint": a.Fingerprint,
		"status":      a.Status,
		"labels":      decodeAlertJSON(a.Labels),
		"annotations": decodeAlertJSON(a.Annotations),
		"starts_at":   a.StartsAt,
		"ends_at":     a.EndsAt,
		"updated_at":  a.UpdatedAt,
		"created_at":  a.CreatedAt,
	}
}

// decodeAlertJSON 存储的 JSON 文本 → map 对象（反序列化失败返回 nil）
func decodeAlertJSON(s string) map[string]string {
	if s == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}
