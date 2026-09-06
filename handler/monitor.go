package handler

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// MonitorHandler 监控处理器：代理 Alertmanager 当前告警列表，供前端"监控中心"页展示。
// 经后端转发而非浏览器直连，规避跨域，也避免暴露 Alertmanager 地址与端口。
type MonitorHandler struct {
	Client          *http.Client
	AlertmanagerURL string
}

// NewMonitorHandler 创建监控处理器（alertmanagerURL 来自 config.ALERTMANAGER_URL）。
func NewMonitorHandler(alertmanagerURL string) *MonitorHandler {
	return &MonitorHandler{
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
