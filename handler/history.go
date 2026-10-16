package handler

// history.go 历史性能曲线的 Prometheus 客户端核心：把 Prometheus 当历史库，经 query_range
// 拉取过去 N 分钟的指标序列。含通用序列/单点数据结构、query_range 调用与序列解析、
// 曲线合成与数值取整等共享工具。具体曲线端点见 history_handlers.go，
// 通用只读 PromQL 查询端点见 history_query.go。

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// HistoryHandler 历史性能曲线处理器：把 Prometheus 当历史库，
// 经 query_range 拉取过去 N 分钟的指标序列，前端进页面即可画满曲线
// （替代原先"浏览器内存里从零攒点、刷新即失"的行为）。
type HistoryHandler struct {
	DB            *gorm.DB
	PrometheusURL string
	Client        *http.Client
}

// NewHistoryHandler 创建历史曲线处理器（prometheusURL 来自 config.PROMETHEUS_URL）。
func NewHistoryHandler(db *gorm.DB, prometheusURL string) *HistoryHandler {
	return &HistoryHandler{
		DB:            db,
		PrometheusURL: prometheusURL,
		Client:        &http.Client{Timeout: 3 * time.Second},
	}
}

// tsPoint 单指标序列点：T 为 HH:MM:SS 文案（与前端轮询追加的点位格式一致）。
// ⚠️ 必须带 json tag（小写 t/val）：前端按 pt.t / pt.val 读取。曾漏 tag 导致序列化为
// 大写 T/Val，所有基于 tsPoint 的曲线（存储池使用率、任务队列深度、抓取目标存活、
// 运行虚拟机数趋势、VM 六指标）x 轴全渲染成 "undefined"、series 无值（2026-10-13 修）。
type tsPoint struct {
	T   string  `json:"t"`
	Val float64 `json:"val"`
}

// histPoint 历史曲线单点（CPU 与内存占比两条序列合并后）。
type histPoint struct {
	T   string  `json:"t"`
	CPU float64 `json:"cpu"`
	Mem float64 `json:"mem"`
}

// tsSeries 一条序列：标签 + 时间点列。
type tsSeries struct {
	Labels map[string]string
	Points []tsPoint
}

// queryRangeMulti 调 Prometheus query_range，返回全部序列（按标签分组）。
// Prometheus 不可达/查询失败返回错误（调用方降级为空点列，不报 5xx）。
func (h *HistoryHandler) queryRangeMulti(c *gin.Context, query string, minutes int) ([]tsSeries, error) {
	end := time.Now()
	start := end.Add(-time.Duration(minutes) * time.Minute)
	q := url.Values{}
	q.Set("query", query)
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	q.Set("step", "15s") // 与抓取周期一致，一个采样一个点

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet,
		h.PrometheusURL+"/api/v1/query_range?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("监控后端 Prometheus 不可达: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("监控后端 Prometheus 响应异常: %s", resp.Status)
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Values [][]interface{}   `json:"values"` // [unix秒, "数值字符串"]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != "success" {
		return []tsSeries{}, nil // 查询合法但无序列（如 VM 从未运行过）
	}

	series := make([]tsSeries, 0, len(body.Data.Result))
	for _, r := range body.Data.Result {
		points := make([]tsPoint, 0, len(r.Values))
		for _, v := range r.Values {
			ts, ok := v[0].(float64)
			if !ok {
				continue
			}
			valStr, _ := v[1].(string)
			val, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				continue
			}
			// 合成表达式（如 0/0 的内存占比）可能产生 NaN/Inf，Go 的 JSON 不支持直接序列化，
			// 会直接导致响应体写不出来——跳过这些非有限点
			if math.IsNaN(val) || math.IsInf(val, 0) {
				continue
			}
			points = append(points, tsPoint{
				T:   time.Unix(int64(ts), 0).Format("15:04:05"),
				Val: val,
			})
		}
		series = append(series, tsSeries{Labels: r.Metric, Points: points})
	}
	return series, nil
}

// queryRange 调 Prometheus query_range，返回首个序列的时间点列（单序列场景）。
func (h *HistoryHandler) queryRange(c *gin.Context, query string, minutes int) ([]tsPoint, error) {
	series, err := h.queryRangeMulti(c, query, minutes)
	if err != nil {
		return nil, err
	}
	if len(series) == 0 {
		return []tsPoint{}, nil
	}
	return series[0].Points, nil
}

// mergeHist 把 CPU 与内存两条序列合成 histPoint（步长与起止一致，点数理论相同；防御性按最短对齐）。
func mergeHist(cpu, mem []tsPoint) []histPoint {
	points := make([]histPoint, len(cpu))
	for i, p := range cpu {
		points[i] = histPoint{T: p.T, CPU: round1(p.Val)}
	}
	n := len(mem)
	if n > len(points) {
		n = len(points)
	}
	for i := 0; i < n; i++ {
		points[i].Mem = round1(mem[i].Val)
	}
	return points
}

// round1 曲线展示保留一位小数（与前端 pushHistory 的 toFixed(1) 一致）。
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// clampMinutes 解析 minutes 参数：5~360，非法回退默认值。
func clampMinutes(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 5 {
		return def
	}
	if n > 360 {
		return 360
	}
	return n
}

// escapePromLabel 转义 PromQL 字符串字面量中的特殊字符（label 匹配值）。
func escapePromLabel(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '\\', '"':
			out = append(out, '\\', ch)
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, ch)
		}
	}
	return string(out)
}
