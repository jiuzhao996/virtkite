// network_flow.go：网络通信流量视图（P1，2026-10-04）——宿主机/libvirt/Docker 三域
// 网络间的实时通信一览：连接边（ss -tn 采样按网段归类聚合）+ 接口速率
// （/proc/net/dev 两次采样差分）。
//
// GET /api/network/flows（operator+，前端 3s 轮询）
//
// 设计取舍：
//   - ss 不带 -p（进程归属需 root，平台进程可能非 root 运行）——v1 不含进程名；
//   - 分类基准 = libvirt 网络（gateway+netmask → CIDR）∪ Docker 网络（inspect 网段）
//     ∪ 宿主机自身地址 ∪ 「外部」兜底；按最长前缀匹配（/24 优先于 /16），
//     避免 docker 网段落在 172.16/12 私网段时被误归；
//   - 接口速率采样内置 1s sleep：单请求耗时 ≈1s，3s 轮询下可接受；
//     reader 挂在 handler 上以便后续按需注入（v1 直接新起）。
package handler

import (
	"bufio"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/virt"
)

// FlowNetwork 流量视图的网络节点。
type FlowNetwork struct {
	ID     string `json:"id"`     // 稳定 id：libvirt:<name> / docker:<name> / host / external
	Name   string `json:"name"`   // 展示名
	Kind   string `json:"kind"`   // libvirt / docker / host / external
	Subnet string `json:"subnet"` // CIDR（host/external 为空）
	Bridge string `json:"bridge"` // 对应网桥接口名（速率挂靠用；可为空）
}

// FlowEdge 网络↔网络的聚合连接边。
type FlowEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Conns int    `json:"conns"`
}

// FlowInterface 接口实时速率。
type FlowInterface struct {
	Name  string `json:"name"`
	NetID string `json:"net_id"` // 挂靠的网络 id（物理口=host；docker0/br-*=逐口挂 docker 段；未匹配为空）
	RxBps int64  `json:"rx_bps"`
	TxBps int64  `json:"tx_bps"`
}

// NetworkFlowHandler 网络流量视图处理器。
type NetworkFlowHandler struct {
	Virt   *virt.Virt
	Docker *dockerx.Dockerx
}

// NewNetworkFlowHandler 创建流量视图处理器。
func NewNetworkFlowHandler() *NetworkFlowHandler {
	return &NetworkFlowHandler{Virt: virt.New(), Docker: dockerx.New()}
}

// cidrNet 分类用：CIDR + 所属网络 id + 前缀长度（最长前缀优先）。
type cidrNet struct {
	ipnet *net.IPNet
	netID string
	plen  int
}

// buildFlowNets 汇总全部网络定义（libvirt ∪ docker ∪ host ∪ external）。
// libvirt 网络拿 gateway + netmask（255.255.255.0 形态）合成网络 CIDR。
func (h *NetworkFlowHandler) buildFlowNets() ([]FlowNetwork, []cidrNet) {
	nets := []FlowNetwork{
		{ID: "host", Name: "宿主机", Kind: "host"},
		{ID: "external", Name: "外部", Kind: "external"},
	}
	cidrs := []cidrNet{}

	// 宿主机自身地址 → host（最长前缀同样参与，本机 IP 精确 /32）
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				cidrs = append(cidrs, cidrNet{ipnet: ipnet, netID: "host", plen: 32})
			}
		}
	}

	// libvirt 网络（inactive 网络没有运行态网段，跳过： inactive 下无流量可言）
	if list, err := h.Virt.ListNetworks(); err == nil {
		for _, n := range list {
			if !n.Active || n.Gateway == "" || n.CIDR == "" {
				continue
			}
			mask := net.IPMask(net.ParseIP(n.CIDR).To4())
			if mask == nil {
				continue
			}
			ipnet := &net.IPNet{IP: net.ParseIP(n.Gateway).Mask(mask), Mask: mask}
			ones, _ := ipnet.Mask.Size()
			id := "libvirt:" + n.Name
			nets = append(nets, FlowNetwork{ID: id, Name: n.Name, Kind: "libvirt", Subnet: ipnet.String(), Bridge: n.Bridge})
			cidrs = append(cidrs, cidrNet{ipnet: ipnet, netID: id, plen: ones})
		}
	}

	// Docker 网络（inspect 批量取网段；Docker 未运行时静默跳过——DockerGate 语义由前端按需处理）
	if h.Docker != nil {
		if list, err := h.Docker.Networks(); err == nil && len(list) > 0 {
			names := make([]string, 0, len(list))
			for _, n := range list {
				names = append(names, n.Name)
			}
			details, _ := h.Docker.NetworkDetails(names)
			for _, n := range list {
				id := "docker:" + n.Name
				fn := FlowNetwork{ID: id, Name: n.Name, Kind: "docker"}
				if d, ok := details[n.Name]; ok && d.Subnet != "" {
					if _, ipnet, err := net.ParseCIDR(d.Subnet); err == nil {
						fn.Subnet = d.Subnet
						ones, _ := ipnet.Mask.Size()
						cidrs = append(cidrs, cidrNet{ipnet: ipnet, netID: id, plen: ones})
					}
				}
				nets = append(nets, fn)
			}
		}
	}
	return nets, cidrs
}

// classifyIP 按 CIDR 归网（最长前缀匹配；未命中 → external）。
func classifyIP(ip net.IP, cidrs []cidrNet) string {
	best := ""
	bestPlen := -1
	for _, c := range cidrs {
		if c.ipnet.Contains(ip) && c.plen > bestPlen {
			best = c.netID
			bestPlen = c.plen
		}
	}
	if best == "" {
		return "external"
	}
	return best
}

// sampleSS 采样 TCP 连接表（ss -tn，无需 root），返回聚合边。
func sampleSS(cidrs []cidrNet) []FlowEdge {
	type key struct{ from, to string }
	agg := map[key]int{}

	f, err := os.Open("/proc/net/tcp")
	if err != nil {
		return []FlowEdge{}
	}
	defer f.Close()
	// /proc/net/tcp 与 ss 同源（socket 内核表），免起子进程：
	// 列 = sl local_address rem_address st ...；st=01 为 ESTABLISHED。
	// 地址形态为十六进制 IPv4/IPv6（如 0100007F:0CEA）。
	scanner := bufio.NewScanner(f)
	scanner.Scan() // 表头（实际无表头，首行即数据——Scan 掉无害，循环里防御空行）
	for i := 0; scanner.Scan(); i++ {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || i == 0 {
			continue
		}
		if fields[3] != "01" { // ESTABLISHED
			continue
		}
		local := hexToIP(fields[1])
		remote := hexToIP(fields[2])
		if local == nil || remote == nil {
			continue
		}
		// 环回↔环回滤除：TOP 边会被纯本机 chatter（浏览器/IDE 的 127.x 连接）刷屏
		if local.IsLoopback() && remote.IsLoopback() {
			continue
		}
		k := key{from: classifyIP(local, cidrs), to: classifyIP(remote, cidrs)}
		agg[k]++
	}
	// 环回连接（127/::1 两端都归 host）会刷出 host→host 大边，聚合无方向后仍保留一条
	// ——v1 如实展示（平台内部通信也是通信）。

	edges := make([]FlowEdge, 0, len(agg))
	for k, n := range agg {
		edges = append(edges, FlowEdge{From: k.from, To: k.to, Conns: n})
	}
	return edges
}

// hexToIP 解析 /proc/net/tcp 的十六进制地址（小端序 IPv4：0100007F = 127.0.0.1）。
func hexToIP(s string) net.IP {
	hostPart, _, ok := strings.Cut(s, ":")
	if !ok || len(hostPart) == 0 {
		return nil
	}
	b, err := strconv.ParseUint("0x"+hostPart, 0, 64)
	if err != nil {
		return nil
	}
	switch len(hostPart) {
	case 8: // IPv4
		return net.IPv4(byte(b), byte(b>>8), byte(b>>16), byte(b>>24))
	case 32: // IPv6（按内核 4 字节组小端 + 组间大端；本项目环境以 v4 为主，粗解即可）
		ip := make(net.IP, 16)
		for i := 0; i < 4; i++ {
			seg, err := strconv.ParseUint(hostPart[i*8:(i+1)*8], 16, 32)
			if err != nil {
				return nil
			}
			ip[i*4], ip[i*4+1], ip[i*4+2], ip[i*4+3] = byte(seg), byte(seg>>8), byte(seg>>16), byte(seg>>24)
		}
		return ip
	}
	return nil
}

// sampleDevRates /proc/net/dev 两次采样差分（1s），返回接口 rx/tx 速率（Bps）。
func sampleDevRates() map[string][2]int64 {
	read := func() map[string][2]int64 {
		out := map[string][2]int64{}
		f, err := os.Open("/proc/net/dev")
		if err != nil {
			return out
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for i := 0; scanner.Scan(); i++ {
			if i < 2 { // 两行表头
				continue
			}
			line := scanner.Text()
			name, rest, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			fields := strings.Fields(rest)
			if len(fields) < 10 {
				continue
			}
			rx, _ := strconv.ParseInt(fields[0], 10, 64)
			tx, _ := strconv.ParseInt(fields[8], 10, 64)
			out[strings.TrimSpace(name)] = [2]int64{rx, tx}
		}
		return out
	}

	a := read()
	time.Sleep(time.Second)
	b := read()
	rates := map[string][2]int64{}
	for name, vb := range b {
		if va, ok := a[name]; ok {
			rates[name] = [2]int64{vb[0] - va[0], vb[1] - va[1]} // 1s 窗口：字节数即 Bps
		}
	}
	return rates
}

// Flows GET /api/network/flows —— 网络/边/接口速率三段式响应。
func (h *NetworkFlowHandler) Flows(c *gin.Context) {
	nets, cidrs := h.buildFlowNets()

	var edges []FlowEdge
	var rates map[string][2]int64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); edges = sampleSS(cidrs) }()
	go func() { defer wg.Done(); rates = sampleDevRates() }()
	wg.Wait()

	// 接口挂靠：virbr* → 同名 bridge 的 libvirt 网络；docker0/br-* → docker 段（逐口，
	// br-<id> 与具体 docker 网络的映射需 inspect Bridge 字段，v1 统挂第一个 docker 网络的
	// 段名展示为「Docker」聚合——精确映射列 v2）；物理口 → host。
	bridgeToNet := map[string]string{}
	firstDocker := ""
	for _, n := range nets {
		if n.Kind == "libvirt" && n.Bridge != "" {
			bridgeToNet[n.Bridge] = n.ID
		}
		if n.Kind == "docker" && firstDocker == "" {
			firstDocker = n.ID
		}
	}
	ifaces := make([]FlowInterface, 0, len(rates))
	for name, r := range rates {
		netID := ""
		switch {
		case strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") || name == "docker0":
			if id, ok := bridgeToNet[name]; ok {
				netID = id
			} else if firstDocker != "" {
				netID = firstDocker
			}
		case name == "lo":
			continue // 环回速率无图上意义
		default:
			netID = "host"
		}
		ifaces = append(ifaces, FlowInterface{Name: name, NetID: netID, RxBps: r[0], TxBps: r[1]})
	}

	Success(c, gin.H{
		"networks":   nets,
		"edges":      edges,
		"interfaces": ifaces,
		"sampled_at": time.Now().Format("15:04:05"),
	})
}
