package handler

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
type tsPoint struct {
	T   string
	Val float64
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
		return nil, fmt.Errorf("Prometheus 不可达: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Prometheus 响应异常: %s", resp.Status)
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

// HostHistory 宿主机历史曲线 GET /api/dashboard/host-history?minutes=60。
// 返回 {points: [{t,cpu,mem}]}，供仪表盘进页面时预填 60 点环形序列。
func (h *HistoryHandler) HostHistory(c *gin.Context) {
	minutes := clampMinutes(c.Query("minutes"), 60)

	cpu, err := h.queryRange(c, "vmops_host_cpu_percent", minutes)
	if err != nil {
		LogError(c, fmt.Errorf("查询宿主机 CPU 历史: %w", err))
		Success(c, gin.H{"points": []histPoint{}, "source": "unavailable"})
		return
	}
	mem, err := h.queryRange(c, "100 * vmops_host_mem_used_kib / vmops_host_mem_total_kib", minutes)
	if err != nil {
		LogError(c, fmt.Errorf("查询宿主机内存历史: %w", err))
		Success(c, gin.H{"points": []histPoint{}, "source": "unavailable"})
		return
	}
	Success(c, gin.H{"points": mergeHist(cpu, mem), "source": "prometheus"})
}

// VMStatsHistory 虚拟机历史曲线 GET /api/vms/:id/stats-history?minutes=30。
// 内存占比优先 host 视角（balloon），与 Grafana 看板一致。
func (h *HistoryHandler) VMStatsHistory(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm struct {
		Name string `gorm:"column:name"`
	}
	if err := h.DB.Table("vms").Select("name").Where("id = ?", id).Take(&vm).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}

	minutes := clampMinutes(c.Query("minutes"), 30)
	// vm 标签值经 Prometheus 转义后拼进 PromQL 选择器（名称白名单已在创建时约束，此处再防御）
	vmLabel := escapePromLabel(vm.Name)
	cpu, err := h.queryRange(c, fmt.Sprintf(`vmops_vm_cpu_percent{vm=%q}`, vmLabel), minutes)
	if err != nil {
		LogError(c, fmt.Errorf("查询 VM CPU 历史: %w", err))
		Success(c, gin.H{"points": []histPoint{}, "source": "unavailable"})
		return
	}
	mem, err := h.queryRange(c,
		fmt.Sprintf(`100 * vmops_vm_mem_used_kib{vm=%q} / vmops_vm_mem_total_kib{vm=%q}`, vmLabel, vmLabel), minutes)
	if err != nil {
		LogError(c, fmt.Errorf("查询 VM 内存历史: %w", err))
		Success(c, gin.H{"points": []histPoint{}, "source": "unavailable"})
		return
	}
	Success(c, gin.H{"points": mergeHist(cpu, mem), "source": "prometheus"})
}

// VMsHistory 全部虚拟机历史曲线 GET /api/dashboard/vm-history?minutes=30。
// 返回 {vms: {vm名: [{t,cpu,mem}]}}，供虚拟机列表页一次拉全所有卡片的迷你曲线历史。
func (h *HistoryHandler) VMsHistory(c *gin.Context) {
	minutes := clampMinutes(c.Query("minutes"), 30)

	cpuSeries, err := h.queryRangeMulti(c, "vmops_vm_cpu_percent", minutes)
	if err != nil {
		LogError(c, fmt.Errorf("查询 VM CPU 历史: %w", err))
		Success(c, gin.H{"vms": map[string][]histPoint{}, "source": "unavailable"})
		return
	}
	memSeries, err := h.queryRangeMulti(c,
		"100 * vmops_vm_mem_used_kib / vmops_vm_mem_total_kib", minutes)
	if err != nil {
		LogError(c, fmt.Errorf("查询 VM 内存历史: %w", err))
		Success(c, gin.H{"vms": map[string][]histPoint{}, "source": "unavailable"})
		return
	}

	// 按 vm 标签分组合并 CPU 与内存两条序列
	vms := map[string][]histPoint{}
	for _, s := range cpuSeries {
		name := s.Labels["vm"]
		points := make([]histPoint, len(s.Points))
		for i, p := range s.Points {
			points[i] = histPoint{T: p.T, CPU: round1(p.Val)}
		}
		vms[name] = points
	}
	for _, s := range memSeries {
		name := s.Labels["vm"]
		points, ok := vms[name]
		if !ok {
			continue
		}
		n := len(s.Points)
		if n > len(points) {
			n = len(points)
		}
		for i := 0; i < n; i++ {
			points[i].Mem = round1(s.Points[i].Val)
		}
	}

	// 剔除空序列（关机 VM 无采样，前端无需为它画图）
	filtered := map[string][]histPoint{}
	for name, pts := range vms {
		if len(pts) > 0 {
			filtered[name] = pts
		}
	}
	Success(c, gin.H{"vms": filtered, "source": "prometheus"})
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
