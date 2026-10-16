package handler

// history_query.go 通用只读 PromQL 查询端点：前端监控面板注册表据此动态渲染任意面板，
// 加一张面板 = 注册表加一行，零后端改动。instant（单点）/ range（区间序列）两种模式，
// 入口钳制查询长度与时间跨度、出口截断序列数，防昂贵正则与超大响应。

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// promQueryLimit 通用查询端点的防护钳制：PromQL 天然只读（无写语义），真正要防的是
// 昂贵正则与超大响应——查询长度、时间跨度在入口钳制，序列数在出口截断。
const (
	promQueryMaxLen    = 512
	promQueryMaxSeries = 50
)

// PromQuery 通用只读查询 GET /api/monitor/prom-query?query=..&minutes=..
// minutes 缺省 / <=0 = instant 即时查询（单点），>0 = range 区间序列。
// 供前端 panels.js 注册表消费：加一张监控面板 = 注册表加一行，零后端改动。
// 走 GET（原为 POST）：审计中间件对 GET 放行，否则每次开监控页的多次面板查询都被记成
// access 审计、把仪表盘「最近动态」刷屏（2026-10-13 修）；只读查询本就是 GET 语义。
func (h *HistoryHandler) PromQuery(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		Fail(c, http.StatusBadRequest, "query 不能为空")
		return
	}
	if len(query) > promQueryMaxLen {
		Fail(c, http.StatusBadRequest, "查询表达式过长（上限 512 字符）")
		return
	}
	minutes, _ := strconv.Atoi(c.Query("minutes"))

	// instant：/api/v1/query 单点查询，统一包装成单点序列（前端渲染口径一致）
	if minutes <= 0 {
		val, labels, err := h.instantQuery(c, query)
		if err != nil {
			LogError(c, fmt.Errorf("监控后端 Prometheus instant 查询失败: %w", err))
			Success(c, gin.H{"series": []gin.H{}, "source": "unavailable"})
			return
		}
		series := []gin.H{}
		if val != nil {
			series = append(series, gin.H{"labels": labels, "points": []tsPoint{*val}})
		}
		Success(c, gin.H{"series": series, "source": "prometheus"})
		return
	}

	series, err := h.queryRangeMulti(c, query, clampMinutes(c.Query("minutes"), 30))
	if err != nil {
		LogError(c, fmt.Errorf("监控后端 Prometheus range 查询失败: %w", err))
		Success(c, gin.H{"series": []gin.H{}, "source": "unavailable"})
		return
	}
	out := make([]gin.H, 0, len(series))
	for _, s := range series {
		out = append(out, gin.H{"labels": s.Labels, "points": s.Points})
	}
	if len(out) > promQueryMaxSeries {
		out = out[:promQueryMaxSeries]
	}
	Success(c, gin.H{"series": out, "source": "prometheus"})
}

// instantQuery 调 Prometheus /api/v1/query，返回首个序列的最新值与标签（无序列返回 nil,nil,nil）。
func (h *HistoryHandler) instantQuery(c *gin.Context, query string) (*tsPoint, map[string]string, error) {
	q := url.Values{}
	q.Set("query", query)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet,
		h.PrometheusURL+"/api/v1/query?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("监控后端 Prometheus 不可达: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("监控后端 Prometheus 响应异常: %s", resp.Status)
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"` // [unix秒, "数值字符串"]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, nil, err
	}
	if body.Status != "success" || len(body.Data.Result) == 0 {
		return nil, nil, nil
	}
	r := body.Data.Result[0]
	valStr, _ := r.Value[1].(string)
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
		return nil, nil, nil
	}
	ts, _ := r.Value[0].(float64)
	point := tsPoint{
		T:   time.Unix(int64(ts), 0).Format("15:04:05"),
		Val: round1(val),
	}
	return &point, r.Metric, nil
}
