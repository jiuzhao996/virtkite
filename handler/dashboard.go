package handler

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// hostCPUStat / hostCPUPrev 缓存上一次 /proc/stat 的 cpu 行累加值，用于差分计算 CPU 百分比。
// 参考 service/virt/stats.go 的滚动采样写法。
// /proc/stat 给的是开机以来的累计 jiffies，只有两次采样求差才能得到「当前占用率」；
// 缓存全局共享（同一时刻只有一个宿主机），必须持 hostStatsMu 串行访问。
var (
	hostStatsMu sync.Mutex
	hostCPUPrev struct {
		idle  uint64
		total uint64
		at    time.Time
	}
)

// DashboardHandler 仪表盘统计处理器。
// DB 用于计数/分布/活动流；Virt 用于实时存储池/网络计数与 VM 性能采样。
type DashboardHandler struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewDashboardHandler 创建仪表盘处理器。
func NewDashboardHandler(db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{DB: db, Virt: virt.New()}
}

// Overview 平台总览统计。
// 计数全部 best-effort：任一查询失败只把该项留 0 并记日志，不整页 500——
// 仪表盘是「概览」而非「账本」，一个统计失败不应让用户看不到其余数据。
func (h *DashboardHandler) Overview(c *gin.Context) {
	var hostCount, vmCount, runningVMCount, imageCount, userCount, auditCount int64

	// 仪表盘计数 best-effort：单个失败不阻断，但留痕避免"静默全 0"被当成无负载
	counts := []struct {
		name string
		err  error
	}{
		{"hosts", h.DB.Model(&model.Host{}).Count(&hostCount).Error},
		{"vms", h.DB.Model(&model.VM{}).Count(&vmCount).Error},
		{"running", h.DB.Model(&model.VM{}).Where("status = ?", model.VMStatusRunning).Count(&runningVMCount).Error},
		{"images", h.DB.Model(&model.Image{}).Count(&imageCount).Error},
		{"users", h.DB.Model(&model.User{}).Count(&userCount).Error},
		{"audit", h.DB.Model(&model.AuditLog{}).Count(&auditCount).Error},
	}
	for _, c := range counts {
		if c.err != nil {
			log.Printf("[dashboard] 计数查询失败 %s: %v", c.name, c.err)
		}
	}

	// 存储池 / 网络计数（实时从 libvirt 获取，失败则置 0）
	poolCount := 0
	networkCount := 0
	if pools, err := h.Virt.ListPools(); err == nil {
		poolCount = len(pools)
	}
	if nets, err := h.Virt.ListNetworks(); err == nil {
		networkCount = len(nets)
	}

	Success(c, gin.H{
		"host_count":       hostCount,
		"vm_count":         vmCount,
		"running_vm_count": runningVMCount,
		"image_count":      imageCount,
		"user_count":       userCount,
		"audit_count":      auditCount,
		"pool_count":       poolCount,
		"network_count":    networkCount,
	})
}

// VMStatusDistribution 虚拟机状态分布。
// 直接 GROUP BY status 让 DB 聚合；空表时返回 [] 而非 null，前端图表无需判空。
func (h *DashboardHandler) VMStatusDistribution(c *gin.Context) {
	type statusCount struct {
		Status string `json:"status"`
		Count  int64  `json:"count"`
	}

	var result []statusCount
	if err := h.DB.Model(&model.VM{}).
		Select("status, count(*) as count").
		Group("status").
		Scan(&result).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if result == nil {
		result = []statusCount{}
	}

	Success(c, result)
}

// readProcCpuStat 解析 /proc/stat 首行（cpu 聚合行），返回 idle 与 total 累加值（单位 jiffies）。
// idle 口径取 idle+iowait：iowait 期间 CPU 实际空闲（在等 IO），算进「忙」会让
// 磁盘压力误显示为 CPU 高负载。
func readProcCpuStat() (idle, total uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		// "cpu " 带空格前缀，区别于 "cpu0"/"cpu1" 单核行
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// 字段序：user nice system idle iowait irq softirq steal guest guest_nice
		var vals []uint64
		for _, f := range fields[1:] {
			v, perr := strconv.ParseUint(f, 10, 64)
			if perr != nil {
				continue
			}
			vals = append(vals, v)
		}
		if len(vals) < 4 {
			continue
		}
		idle = vals[3]
		if len(vals) > 4 {
			idle += vals[4] // idle + iowait
		}
		for _, v := range vals {
			total += v
		}
		return idle, total, nil
	}
	return 0, 0, fmt.Errorf("/proc/stat 未找到 cpu 聚合行")
}

// HostStats 返回宿主机实时 CPU/内存（读 /proc/stat 差分 + /proc/meminfo）。
// 只读宿主自身，不经过 libvirt，因此即使虚拟化连接异常也能给出系统负载。
func (h *DashboardHandler) HostStats(c *gin.Context) {
	idle, total, err := readProcCpuStat()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取 /proc/stat 失败", err)
		return
	}

	cpuPercent := 0.0
	now := time.Now()
	// 锁内完成「读旧值 → 算差分 → 写新值」，保证并发请求不会拿到错位的采样对
	hostStatsMu.Lock()
	first := hostCPUPrev.at.IsZero()
	if !first {
		dIdle := idle - hostCPUPrev.idle
		dTotal := total - hostCPUPrev.total
		dt := now.Sub(hostCPUPrev.at).Seconds()
		// 计数器回绕或时间片异常时按 0 处理
		if dTotal > 0 && dt > 0 {
			cpuPercent = (1 - float64(dIdle)/float64(dTotal)) * 100
			// 无符号相减在计数器回绕时会得到巨大值，必须夹紧到 [0,100]
			if cpuPercent < 0 {
				cpuPercent = 0
			}
			if cpuPercent > 100 {
				cpuPercent = 100
			}
		}
	}
	hostCPUPrev.idle = idle
	hostCPUPrev.total = total
	hostCPUPrev.at = now
	hostStatsMu.Unlock()

	// 首次调用无历史样本，无法差分：短暂等待后再采一次，避免首屏 CPU 曲线恒 0。
	// 200ms 是「够采到变化」与「不显著拖慢首屏」之间的折中；等待期间不持锁，
	// 否则会阻塞其它并发请求。
	if first {
		time.Sleep(200 * time.Millisecond)
		if idle2, total2, err := readProcCpuStat(); err == nil {
			dTotal := total2 - total
			dIdle := idle2 - idle
			if dTotal > 0 {
				cpuPercent = (1 - float64(dIdle)/float64(dTotal)) * 100
				if cpuPercent < 0 {
					cpuPercent = 0
				}
				if cpuPercent > 100 {
					cpuPercent = 100
				}
			}
		}
	}

	// /proc/meminfo：MemTotal / MemAvailable（kB），used = total - available。
	// 用 MemAvailable 而非 MemFree：后者不含可回收的 page cache，会严重高估占用。
	memTotal, memAvailable := uint64(0), uint64(0)
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			switch {
			case strings.HasPrefix(line, "MemTotal:"):
				memTotal = parseMeminfoKB(line)
			case strings.HasPrefix(line, "MemAvailable:"):
				memAvailable = parseMeminfoKB(line)
			}
		}
	}
	memUsed := uint64(0)
	if memTotal > memAvailable {
		memUsed = memTotal - memAvailable
	}

	Success(c, gin.H{
		"cpu_percent":   cpuPercent,
		"mem_total_kib": memTotal,
		"mem_used_kib":  memUsed,
	})
}

// parseMeminfoKB 解析 /proc/meminfo 行首数值（kB）。
// 行格式如 "MemTotal:       16384000 kB"，第二个字段即数值；解析失败返回 0
// （调用方按「拿不到」处理，不因此报错）。
func parseMeminfoKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// Activity 全站活动流：最近 N 条审计记录（只含写操作——审计中间件不记 GET，天然免刷屏）。
// 挂在 dashboard 组（OperatorMiddleware）而非放权 /api/audit：审计全量查询（筛选/导出）
// 仍 admin 专属，活动流只是脱敏摘要（固定字段、无 IP 明细），权限面最小化。
// limit 做上限收敛（1-100），防止一次拉全表。
func (h *DashboardHandler) Activity(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var logs []model.AuditLog
	if err := h.DB.Order("created_at DESC").Limit(limit).Find(&logs).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]gin.H, 0, len(logs))
	for _, l := range logs {
		items = append(items, gin.H{
			"id":          l.ID,
			"action":      l.Action,
			"object_type": l.ObjectType,
			"object_id":   l.ObjectID,
			"username":    l.Username,
			"status":      l.Status,
			"detail":      l.Detail,
			"created_at":  l.CreatedAt,
		})
	}
	Success(c, gin.H{"items": items, "total": len(items)})
}

// VmPerf 返回各 VM 实时性能（遍历 DB，仅 running 采样 GetDomainStats）。
// 只采样 running：关机域没有可读的 balloon/CPU 统计，逐个调用只会白等超时。
// 单台采样失败按 0 展示，不影响其余 VM（仪表盘是概览，不因一台异常整页失败）。
func (h *DashboardHandler) VmPerf(c *gin.Context) {
	var vms []model.VM
	if err := h.DB.Find(&vms).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询虚拟机失败", err)
		return
	}

	items := make([]gin.H, 0, len(vms))
	for _, vm := range vms {
		if vm.Status != model.VMStatusRunning {
			continue
		}
		cpuPercent, memPct := 0.0, 0.0
		if st, err := h.Virt.GetDomainStats(vm.Name); err == nil && st != nil {
			cpuPercent = st.CpuPercent
			// 优先 balloon 口径（GuestUsed/GuestTotal），缺失时回退分配内存口径（MemUsed/MemTotal）
			if st.GuestTotalKiB > 0 {
				memPct = float64(st.GuestUsedKiB) / float64(st.GuestTotalKiB) * 100
			} else if st.MemTotalKiB > 0 {
				memPct = float64(st.MemUsedKiB) / float64(st.MemTotalKiB) * 100
			}
		}
		items = append(items, gin.H{
			"id":          vm.ID,
			"name":        vm.Name,
			"status":      model.VMStatusRunning,
			"cpu_percent": cpuPercent,
			"mem_pct":     memPct,
		})
	}

	Success(c, items)
}
