package metrics

// vmops Prometheus 内建 exporter（自研，替代独立 exporter 进程）。
// 采集 libvirt 域指标 + 宿主机 + 存储池 + 任务队列，Prometheus 定时 scrape /metrics。
// label 仅用 VM 名/池名（有界值，符合低基数要求）；抓取失败跳过，保留上次值。

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

var (
	// VM CPU 使用率（%）。
	// PromQL: topk(5, vmops_vm_cpu_percent)
	vmCPU = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vmops_vm_cpu_percent",
		Help: "VM CPU 使用率（服务端差分，百分比）",
	}, []string{"vm"})

	// VM 内存占用（KiB）。
	// PromQL: vmops_vm_mem_used_kib / vmops_vm_mem_total_kib
	vmMemUsed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vmops_vm_mem_used_kib",
		Help: "VM 已用内存 KiB（balloon 优先）",
	}, []string{"vm"})
	vmMemTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vmops_vm_mem_total_kib",
		Help: "VM 总内存 KiB",
	}, []string{"vm"})

	// VM 磁盘 IO（B/s，首盘口径）。
	// PromQL: rate(vmops_vm_disk_read_bytes_total[5m]) （如需累计口径可另加 Counter）
	vmDiskRead = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vmops_vm_disk_read_bps",
		Help: "VM 磁盘读速率 B/s",
	}, []string{"vm"})
	vmDiskWrite = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vmops_vm_disk_write_bps",
		Help: "VM 磁盘写速率 B/s",
	}, []string{"vm"})

	// VM 网络 IO（B/s，首网卡口径）。
	// PromQL: vmops_vm_net_rx_bps{vm="node1"}
	vmNetRx = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vmops_vm_net_rx_bps",
		Help: "VM 网络接收速率 B/s",
	}, []string{"vm"})
	vmNetTx = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vmops_vm_net_tx_bps",
		Help: "VM 网络发送速率 B/s",
	}, []string{"vm"})

	// VM 运行状态（1=running）。
	// PromQL: sum(vmops_vm_running) / count(vmops_vm_running)
	vmRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vmops_vm_running",
		Help: "VM 是否运行中（1/0）",
	}, []string{"vm"})

	// 宿主机 CPU/内存。
	// PromQL: vmops_host_cpu_percent / vmops_host_mem_used_kib
	hostCPU      = prometheus.NewGauge(prometheus.GaugeOpts{Name: "vmops_host_cpu_percent", Help: "宿主机 CPU 使用率"})
	hostMemUsed  = prometheus.NewGauge(prometheus.GaugeOpts{Name: "vmops_host_mem_used_kib", Help: "宿主机已用内存 KiB"})
	hostMemTotal = prometheus.NewGauge(prometheus.GaugeOpts{Name: "vmops_host_mem_total_kib", Help: "宿主机总内存 KiB"})

	// 存储池容量（字节）。
	// PromQL: 1 - vmops_pool_available_bytes / vmops_pool_capacity_bytes
	poolCapacity = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vmops_pool_capacity_bytes",
		Help: "存储池总容量字节",
	}, []string{"pool"})
	poolAvailable = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vmops_pool_available_bytes",
		Help: "存储池可用字节",
	}, []string{"pool"})

	// 任务队列深度。
	// PromQL: vmops_tasks_pending + vmops_tasks_running
	tasksPending = prometheus.NewGauge(prometheus.GaugeOpts{Name: "vmops_tasks_pending", Help: "等待中任务数"})
	tasksRunning = prometheus.NewGauge(prometheus.GaugeOpts{Name: "vmops_tasks_running", Help: "执行中任务数"})
)

// Registry 独立注册表（避免 default 全局注册；附带 Go/进程运行时指标）。
var Registry = newRegistry()

func newRegistry() *prometheus.Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		vmCPU, vmMemUsed, vmMemTotal, vmDiskRead, vmDiskWrite,
		vmNetRx, vmNetTx, vmRunning,
		hostCPU, hostMemUsed, hostMemTotal,
		poolCapacity, poolAvailable,
		tasksPending, tasksRunning,
	)
	return r
}

var (
	hostMu       sync.Mutex
	hostPrevIdle uint64
	hostPrevTot  uint64
	hostPrevAt   time.Time
	knownVMs     = map[string]bool{}
	knownVMsMu   sync.Mutex
	knownPools   = map[string]bool{}
	knownPoolsMu sync.Mutex
)

// Collector 指标采集器（每次 scrape 前 Update 刷新）。
type Collector struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewCollector 创建采集器。
func NewCollector(db *gorm.DB) *Collector {
	return &Collector{DB: db, Virt: virt.New()}
}

// Update 刷新全部指标（单次 scrape 调用一次；失败项跳过保留旧值）。
func (c *Collector) Update() {
	c.updateVMs()
	c.updateHost()
	c.updatePools()
	c.updateTasks()
}

func (c *Collector) updateVMs() {
	var vms []model.VM
	if err := c.DB.Find(&vms).Error; err != nil {
		return
	}
	seen := map[string]bool{}
	for _, vm := range vms {
		if vm.Status != "running" {
			vmRunning.WithLabelValues(vm.Name).Set(0)
			seen[vm.Name] = true
			continue
		}
		st, err := c.Virt.GetDomainStats(vm.Name)
		if err != nil || st == nil {
			continue
		}
		seen[vm.Name] = true
		vmRunning.WithLabelValues(vm.Name).Set(1)
		vmCPU.WithLabelValues(vm.Name).Set(st.CpuPercent)
		memTotal := st.MemTotalKiB
		memUsed := st.MemUsedKiB
		if st.GuestTotalKiB > 0 {
			memTotal = st.GuestTotalKiB
			memUsed = st.GuestUsedKiB
		}
		vmMemUsed.WithLabelValues(vm.Name).Set(float64(memUsed))
		vmMemTotal.WithLabelValues(vm.Name).Set(float64(memTotal))
		vmDiskRead.WithLabelValues(vm.Name).Set(float64(st.DiskReadBps))
		vmDiskWrite.WithLabelValues(vm.Name).Set(float64(st.DiskWriteBps))
		vmNetRx.WithLabelValues(vm.Name).Set(float64(st.NetRxBps))
		vmNetTx.WithLabelValues(vm.Name).Set(float64(st.NetTxBps))
	}
	// 清理已删除 VM 的 stale 序列
	knownVMsMu.Lock()
	for name := range knownVMs {
		if !seen[name] {
			vmCPU.DeleteLabelValues(name)
			vmMemUsed.DeleteLabelValues(name)
			vmMemTotal.DeleteLabelValues(name)
			vmDiskRead.DeleteLabelValues(name)
			vmDiskWrite.DeleteLabelValues(name)
			vmNetRx.DeleteLabelValues(name)
			vmNetTx.DeleteLabelValues(name)
			vmRunning.DeleteLabelValues(name)
			delete(knownVMs, name)
		}
	}
	for name := range seen {
		knownVMs[name] = true
	}
	knownVMsMu.Unlock()
}

func (c *Collector) updateHost() {
	idle, total, err := readCPUStat()
	if err != nil {
		return
	}
	hostMu.Lock()
	if !hostPrevAt.IsZero() && total > hostPrevTot {
		dIdle := idle - hostPrevIdle
		dTotal := total - hostPrevTot
		if dTotal > 0 {
			pct := (1 - float64(dIdle)/float64(dTotal)) * 100
			if pct >= 0 && pct <= 100 {
				hostCPU.Set(pct)
			}
		}
	}
	hostPrevIdle, hostPrevTot, hostPrevAt = idle, total, time.Now()
	hostMu.Unlock()

	memTotal, memAvail := readMeminfo()
	if memTotal > 0 {
		hostMemTotal.Set(float64(memTotal))
		if memTotal > memAvail {
			hostMemUsed.Set(float64(memTotal - memAvail))
		}
	}
}

// readCPUStat 读 /proc/stat 聚合行（idle+iowait, total）。
func readCPUStat() (idle, total uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		f := strings.Fields(line)[1:]
		var vals []uint64
		for _, s := range f {
			v, _ := strconv.ParseUint(s, 10, 64)
			vals = append(vals, v)
		}
		if len(vals) < 5 {
			break
		}
		idle = vals[3]
		if len(vals) > 4 {
			idle += vals[4]
		}
		for _, v := range vals {
			total += v
		}
		return idle, total, nil
	}
	return 0, 0, fmt.Errorf("未找到 cpu 聚合行")
}

// readMeminfo 读 MemTotal/MemAvailable（kB）。
func readMeminfo() (total, avail uint64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, _ = strconv.ParseUint(f[1], 10, 64)
		case "MemAvailable:":
			avail, _ = strconv.ParseUint(f[1], 10, 64)
		}
	}
	return total, avail
}

func (c *Collector) updatePools() {
	infos, err := c.Virt.ListPoolInfos()
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, p := range infos {
		seen[p.Name] = true
		poolCapacity.WithLabelValues(p.Name).Set(float64(p.Capacity))
		poolAvailable.WithLabelValues(p.Name).Set(float64(p.Available))
	}
	knownPoolsMu.Lock()
	for name := range knownPools {
		if !seen[name] {
			poolCapacity.DeleteLabelValues(name)
			poolAvailable.DeleteLabelValues(name)
			delete(knownPools, name)
		}
	}
	for name := range seen {
		knownPools[name] = true
	}
	knownPoolsMu.Unlock()
}

func (c *Collector) updateTasks() {
	var pending, running int64
	c.DB.Model(&model.Task{}).Where("status = ?", "pending").Count(&pending)
	c.DB.Model(&model.Task{}).Where("status = ?", "running").Count(&running)
	tasksPending.Set(float64(pending))
	tasksRunning.Set(float64(running))
}
