package virt

import (
	"fmt"
	"runtime"
	"time"

	"github.com/digitalocean/go-libvirt"
)

// VmStats 虚拟机性能统计（时间序列采样，速率由服务端相邻两次采样差分计算）。
type VmStats struct {
	CpuPercent    float64 `json:"cpu_percent"` // 服务端差分计算
	MemUsedKiB    uint64  `json:"mem_used_kib"`
	MemTotalKiB   uint64  `json:"mem_total_kib"`
	GuestUsedKiB  uint64  `json:"guest_used_kib"` // balloon 口径
	GuestTotalKiB uint64  `json:"guest_total_kib"`
	DiskReadBps   uint64  `json:"disk_read_bps"`
	DiskWriteBps  uint64  `json:"disk_write_bps"`
	NetRxBps      uint64  `json:"net_rx_bps"`
	NetTxBps      uint64  `json:"net_tx_bps"`
}

// statSample 单域上一次采样的原始计数器快照，用于差分计算速率与 CPU 百分比。
type statSample struct {
	cpuTimeNS uint64
	diskRead  uint64
	diskWrite uint64
	netRx     uint64
	netTx     uint64
	at        time.Time
}

// GetDomainStats 返回虚拟机实时性能统计
// （对应 virsh domstats / dommemstat / domblkstat / domifstat）。
// CPU 百分比与磁盘/网络速率基于服务端相邻两次采样的差分计算，首次采样返回 0；
// 未运行或采样瞬时失败时返回空的 VmStats（不 panic），仅连接/不存在等硬错误返回 error。
func (v *Virt) GetDomainStats(name string) (*VmStats, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return nil, fmt.Errorf("虚拟机 %s 不存在: %w", name, err)
	}
	state, maxMem, memory, _, cpuTime, err := l.DomainGetInfo(dom)
	if err != nil {
		// 采样瞬时失败（如虚拟机刚关机），按空统计处理
		return &VmStats{}, nil
	}
	// 未运行（关机/暂停）无法采样，返回空统计
	if libvirt.DomainState(state) != libvirt.DomainRunning {
		return &VmStats{}, nil
	}

	stats := &VmStats{
		MemUsedKiB:  memory,
		MemTotalKiB: maxMem,
	}

	// balloon 口径（Guest 内存）：actual 为客户机总内存，used = actual - unused（对应 virsh dommemstat）
	if mem := v.GetMemoryStats(name); mem["actual"] > 0 {
		stats.GuestTotalKiB = mem["actual"]
		if unused, ok := mem["unused"]; ok && unused < stats.GuestTotalKiB {
			stats.GuestUsedKiB = stats.GuestTotalKiB - unused
		}
	}

	// 磁盘 target 与网卡 mac 从 dumpxml 解析（B1 spec.go 未落盘，复用 ListDomainDevices 的解析方式）
	disks, nics, err := v.ListDomainDevices(name)
	if err != nil {
		// 设备解析失败按空统计处理，保证调用方拿到合法结构
		return &VmStats{}, nil
	}
	var diskRead, diskWrite, netRx, netTx uint64
	// 首个磁盘 target 的累计读写字节（对应 virsh domblkstat）
	if len(disks) > 0 && disks[0].Target != "" {
		_, rdBytes, _, wrBytes, _, err := l.DomainBlockStats(dom, disks[0].Target)
		if err == nil {
			diskRead = uint64(rdBytes)
			diskWrite = uint64(wrBytes)
		}
	}
	// 首个网卡 mac 的累计收发字节（对应 virsh domifstat）
	if len(nics) > 0 && nics[0].Target != "" {
		rxBytes, _, _, _, txBytes, _, _, _, err := l.DomainInterfaceStats(dom, nics[0].Target)
		if err == nil {
			netRx = uint64(rxBytes)
			netTx = uint64(txBytes)
		}
	}

	// 与上一次采样差分计算速率（互斥锁保护缓存）
	now := time.Now()
	v.statsMu.Lock()
	if v.statsCache == nil {
		v.statsCache = make(map[string]*statSample)
	}
	prev, ok := v.statsCache[name]
	v.statsCache[name] = &statSample{
		cpuTimeNS: cpuTime,
		diskRead:  diskRead,
		diskWrite: diskWrite,
		netRx:     netRx,
		netTx:     netTx,
		at:        now,
	}
	v.statsMu.Unlock()

	if ok && prev != nil {
		dt := now.Sub(prev.at).Seconds()
		if dt > 0 {
			stats.DiskReadBps = diffRate(diskRead, prev.diskRead, dt)
			stats.DiskWriteBps = diffRate(diskWrite, prev.diskWrite, dt)
			stats.NetRxBps = diffRate(netRx, prev.netRx, dt)
			stats.NetTxBps = diffRate(netTx, prev.netTx, dt)
			stats.CpuPercent = diffCpuPercent(cpuTime, prev.cpuTimeNS, runtime.NumCPU(), dt)
		}
	}
	return stats, nil
}

// diffRate 计算字节速率（bps）：Δbytes / Δs；计数器回绕或下降时返回 0。
func diffRate(cur, prev uint64, dt float64) uint64 {
	if cur < prev {
		return 0
	}
	return uint64(float64(cur-prev) / dt)
}

// diffCpuPercent 计算 CPU 使用百分比：Δcputime / (hostCPUs * Δs) / 1e9 * 100。
func diffCpuPercent(cur, prev uint64, hostCPUs int, dt float64) float64 {
	if cur < prev || dt <= 0 || hostCPUs <= 0 {
		return 0
	}
	deltaNS := float64(cur - prev)
	return deltaNS / (float64(hostCPUs) * dt * 1e9) * 100
}
