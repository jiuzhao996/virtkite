package handler

import (
	"bufio"
	"fmt"
	"math"
	"net/http"
	"os"
	"runtime"
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
var (
	hostStatsMu sync.Mutex
	hostCPUPrev struct {
		idle  uint64
		total uint64
		at    time.Time
	}
)

// DashboardHandler 仪表盘统计处理器
type DashboardHandler struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewDashboardHandler 创建仪表盘处理器
func NewDashboardHandler(db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{DB: db, Virt: virt.New()}
}

// Overview 平台总览统计
func (h *DashboardHandler) Overview(c *gin.Context) {
	var hostCount, vmCount, runningVMCount, imageCount, userCount, auditCount int64

	h.DB.Model(&model.Host{}).Count(&hostCount)
	h.DB.Model(&model.VM{}).Count(&vmCount)
	h.DB.Model(&model.VM{}).Where("status = ?", "running").Count(&runningVMCount)
	h.DB.Model(&model.Image{}).Count(&imageCount)
	h.DB.Model(&model.User{}).Count(&userCount)
	h.DB.Model(&model.AuditLog{}).Count(&auditCount)

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

// Capacity 资源容量与超分视角：全部 VM 的分配量汇总 vs 宿主机物理容量。
// 单管理节点下宿主机就是平台本机，物理量直接读本机（runtime.NumCPU + /proc/meminfo），
// hosts 表的 cpu_cores/memory_gb 登记字段从未回写、不可靠，仅作读取失败时的兜底。
// cpu_ratio/mem_ratio = 已分配/物理，>1 即超分；物理量取不到时 has_host=false，前端降级展示。
func (h *DashboardHandler) Capacity(c *gin.Context) {
	var alloc struct {
		VCPU   uint64
		MemMB  uint64
		VMClnt int64
	}
	h.DB.Model(&model.VM{}).Select(
		"COALESCE(SUM(v_cpu), 0) AS v_cpu, COALESCE(SUM(memory_mb), 0) AS mem_mb, COUNT(*) AS vm_clnt",
	).Scan(&alloc)

	cores := runtime.NumCPU()
	memKB := uint64(0)
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "MemTotal") {
				memKB = parseMeminfoKB(sc.Text())
				break
			}
		}
		f.Close()
	}
	if cores <= 0 || memKB == 0 {
		// 兜底：本机读数失败时用首台登记宿主机（字段可能为 0，届时 has_host=false）
		var host model.Host
		if err := h.DB.Order("id ASC").First(&host).Error; err == nil {
			cores, memKB = host.CPUCores, uint64(host.MemoryGB*1024)
		}
	}

	resp := gin.H{
		"vm_count":         alloc.VMClnt,
		"allocated_vcpu":   alloc.VCPU,
		"allocated_mem_mb": alloc.MemMB,
		"has_host":         cores > 0 && memKB > 0,
		"physical_cores":   cores,
		"physical_mem_mb":  memKB / 1024,
	}
	if resp["has_host"] == true {
		resp["cpu_ratio"] = math.Round(float64(alloc.VCPU)/float64(cores)*100) / 100
		resp["mem_ratio"] = math.Round(float64(alloc.MemMB)/float64(memKB/1024)*100) / 100
	}
	Success(c, resp)
}

// VMStatusDistribution 虚拟机状态分布
func (h *DashboardHandler) VMStatusDistribution(c *gin.Context) {
	type statusCount struct {
		Status string `json:"status"`
		Count  int64  `json:"count"`
	}

	var result []statusCount
	h.DB.Model(&model.VM{}).
		Select("status, count(*) as count").
		Group("status").
		Scan(&result)

	Success(c, result)
}

// readProcCpuStat 解析 /proc/stat 首行（cpu 聚合行），返回 idle 与 total 累加值（单位 jiffies）。
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
func (h *DashboardHandler) HostStats(c *gin.Context) {
	idle, total, err := readProcCpuStat()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取 /proc/stat 失败", err)
		return
	}

	cpuPercent := 0.0
	now := time.Now()
	hostStatsMu.Lock()
	first := hostCPUPrev.at.IsZero()
	if !first {
		dIdle := idle - hostCPUPrev.idle
		dTotal := total - hostCPUPrev.total
		dt := now.Sub(hostCPUPrev.at).Seconds()
		// 计数器回绕或时间片异常时按 0 处理
		if dTotal > 0 && dt > 0 {
			cpuPercent = (1 - float64(dIdle)/float64(dTotal)) * 100
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

	// 首次调用无历史样本，无法差分：短暂等待后再采一次，避免首屏 CPU 曲线恒 0
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

	// /proc/meminfo：MemTotal / MemAvailable（kB），used = total - available
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

// VmPerf 返回各 VM 实时性能（遍历 DB，仅 running 采样 GetDomainStats）。
func (h *DashboardHandler) VmPerf(c *gin.Context) {
	var vms []model.VM
	if err := h.DB.Find(&vms).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询虚拟机失败", err)
		return
	}

	items := make([]gin.H, 0, len(vms))
	for _, vm := range vms {
		if vm.Status != "running" {
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
			"status":      "running",
			"cpu_percent": cpuPercent,
			"mem_pct":     memPct,
		})
	}

	Success(c, items)
}
