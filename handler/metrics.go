package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jiuzhao/vmops/service/metrics"
	"gorm.io/gorm"
)

// MetricsHandler Prometheus 指标处理器（内建 exporter）。
type MetricsHandler struct {
	Collector *metrics.Collector
	Handler   gin.HandlerFunc
}

// NewMetricsHandler 创建指标处理器（公开路由，无需认证，供 Prometheus scrape）。
func NewMetricsHandler(db *gorm.DB) *MetricsHandler {
	collector := metrics.NewCollector(db)
	prom := promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{})
	return &MetricsHandler{
		Collector: collector,
		Handler: func(c *gin.Context) {
			// 抓取前刷新快照指标；失败项保留旧值，不阻断 exposition
			collector.Update()
			prom.ServeHTTP(c.Writer, c.Request)
		},
	}
}
