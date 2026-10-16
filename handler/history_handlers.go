package handler

// history_handlers.go 各领域历史曲线端点：宿主机、单 VM、全量 VM、存储池、VM 六指标、
// VM 内 guest（node_exporter）指标。均复用 history.go 的 query_range 客户端，Prometheus
// 不可达时降级为 available/unavailable 而非 5xx。

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

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
// 内存占比优先 host 视角（balloon），与监控看板一致。
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
	// 授权决定可见性：非 admin 未持有效授权与不存在同响应
	if !vmVisible(c, h.DB, id) {
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

// PoolHistory 存储池使用率历史 GET /api/monitor/pool-history?minutes=60。
// 返回 {pools: {池名: [{t, val}]}}（val 为已用百分比）——监控中心原生化看板用
// （Grafana 退役批次，替代原 vmops-overview 看板的池使用率面板）。
func (h *HistoryHandler) PoolHistory(c *gin.Context) {
	minutes := clampMinutes(c.Query("minutes"), 60)
	const query = `100 * (vmops_pool_capacity_bytes - vmops_pool_available_bytes) / vmops_pool_capacity_bytes`
	series, err := h.queryRangeMulti(c, query, minutes)
	if err != nil {
		LogError(c, fmt.Errorf("查询存储池使用率历史: %w", err))
		Success(c, gin.H{"pools": map[string][]tsPoint{}, "source": "unavailable"})
		return
	}
	pools := map[string][]tsPoint{}
	for _, s := range series {
		name := s.Labels["pool"]
		if name == "" || len(s.Points) == 0 {
			continue
		}
		pools[name] = s.Points
	}
	Success(c, gin.H{"pools": pools, "source": "prometheus"})
}

// vmMetricSeries 单 VM 的六指标序列（原生看板虚拟机页签对齐：CPU/内存/磁盘读写/网络收发）。
type vmMetricSeries struct {
	CPU       []tsPoint `json:"cpu"`
	Mem       []tsPoint `json:"mem"`        // 内存占用百分比
	DiskRead  []tsPoint `json:"disk_read"`  // 字节/秒
	DiskWrite []tsPoint `json:"disk_write"` // 字节/秒
	NetRx     []tsPoint `json:"net_rx"`     // 字节/秒
	NetTx     []tsPoint `json:"net_tx"`     // 字节/秒
}

// VMDetailedHistory 全部 VM 六指标历史 GET /api/monitor/vm-metrics-history?minutes=30。
// 返回 {vms: {vm名: {cpu,mem,disk_read,disk_write,net_rx,net_tx}}}——监控中心原生化
// 的 VM 明细看板用；磁盘/网络速率与 /metrics 采集同口径（只采样首盘/首网卡）。
func (h *HistoryHandler) VMDetailedHistory(c *gin.Context) {
	minutes := clampMinutes(c.Query("minutes"), 30)
	queries := []struct {
		key  string
		expr string
	}{
		{"cpu", "vmops_vm_cpu_percent"},
		{"mem", "100 * vmops_vm_mem_used_kib / vmops_vm_mem_total_kib"},
		{"disk_read", "vmops_vm_disk_read_bps"},
		{"disk_write", "vmops_vm_disk_write_bps"},
		{"net_rx", "vmops_vm_net_rx_bps"},
		{"net_tx", "vmops_vm_net_tx_bps"},
	}
	vms := map[string]*vmMetricSeries{}
	for _, m := range queries {
		series, err := h.queryRangeMulti(c, m.expr, minutes)
		if err != nil {
			// Prometheus 不可达：整体降级为空（不 5xx），前端显示 unavailable 态
			LogError(c, fmt.Errorf("查询 VM %s 历史: %w", m.key, err))
			Success(c, gin.H{"vms": map[string]*vmMetricSeries{}, "source": "unavailable"})
			return
		}
		for _, s := range series {
			name := s.Labels["vm"]
			if name == "" || len(s.Points) == 0 {
				continue
			}
			row := vms[name]
			if row == nil {
				row = &vmMetricSeries{}
				vms[name] = row
			}
			switch m.key {
			case "cpu":
				row.CPU = s.Points
			case "mem":
				row.Mem = s.Points
			case "disk_read":
				row.DiskRead = s.Points
			case "disk_write":
				row.DiskWrite = s.Points
			case "net_rx":
				row.NetRx = s.Points
			case "net_tx":
				row.NetTx = s.Points
			}
		}
	}
	Success(c, gin.H{"vms": vms, "source": "prometheus"})
}

// GuestMetrics VM 内部指标 GET /api/vms/:id/guest-metrics?minutes=30。
// 消费 file_sd 抓取的 node_exporter 指标（file_sd 目标带 vm_name/vm_id 标签，会附着到
// 该目标抓回的全部序列）：根分区使用率 / guest 内存占用 / load1 三条曲线。
// VM 未装 node_exporter 时 available=false（前端显示安装引导），Prometheus 不可达同样降级。
func (h *HistoryHandler) GuestMetrics(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm struct {
		Name string `gorm:"column:name"`
		IP   string `gorm:"column:ip"`
	}
	if err := h.DB.Table("vms").Select("name, ip").Where("id = ?", id).Take(&vm).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	if !vmVisible(c, h.DB, id) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}

	// file_sd 的目标标签 vm_name 会附着到该目标抓回的全部 node_exporter 序列，按它过滤；
	// 指标自带的选择器（如 mountpoint）必须并进同一个 {}——PromQL 不允许相邻两个选择器
	selector := fmt.Sprintf(`job="vm-node",vm_name=%q`, escapePromLabel(vm.Name))
	queries := []struct {
		key  string
		expr string
	}{
		{"fs", fmt.Sprintf(`100 - 100 * (node_filesystem_avail_bytes{%s,mountpoint="/"} / node_filesystem_size_bytes{%s,mountpoint="/"})`, selector, selector)},
		{"mem", fmt.Sprintf(`100 * (1 - node_memory_MemAvailable_bytes{%s} / node_memory_MemTotal_bytes{%s})`, selector, selector)},
		{"load1", fmt.Sprintf(`node_load1{%s}`, selector)},
	}

	minutes := clampMinutes(c.Query("minutes"), 30)
	out := gin.H{"available": false, "vm": vm.Name, "source": "prometheus"}
	hasData := false
	for _, q := range queries {
		points, err := h.queryRange(c, q.expr, minutes)
		if err != nil {
			LogError(c, fmt.Errorf("查询 guest %s 指标: %w", q.key, err))
			out[q.key] = []tsPoint{}
			out["source"] = "unavailable"
			continue
		}
		if len(points) > 0 {
			hasData = true
		}
		out[q.key] = points
	}
	out["available"] = hasData
	Success(c, out)
}
