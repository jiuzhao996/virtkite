// network_topology.go：全局网络拓扑（N1）——宿主机 → 桥/虚拟网络 → VM/容器 三层关系图。
// 数据全部免 root 可得：libvirt 网络与域网卡（go-libvirt）、DHCP 租约、Docker 网络 inspect
// （网段+挂接容器）、宿主机接口（ip -br addr）、邻居表（ip neigh）、桥接从属（bridge link）。
//
// GET /api/networks/topology（operator+，前端按需刷新）
package handler

import (
	"net"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/hostnet"
	"github.com/jiuzhao/vmops/service/virt"
)

// TopoNode 网络拓扑节点。Kind: host / nic / libvirt_net / docker_net / vm / container
//
// ID 采用「类型前缀 + 业务名」的稳定格式（见字段注释），前端据此做节点去重、
// 边连接与钻取；Name 才是展示名。State/Subtitle/Meta 都是展示辅助字段，
// 不参与图结构。LinkType/LinkID 表示该节点可跳转到的平台资源，前端点击节点
// 时用它打开对应详情页（network/vm/container）。
type TopoNode struct {
	ID       string            `json:"id"` // host / nic:<name> / libvirt:<name> / docker:<name> / vm:<name> / container:<短id>
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	State    string            `json:"state,omitempty"`
	Subtitle string            `json:"subtitle,omitempty"`
	IPs      []string          `json:"ips,omitempty"`
	MAC      string            `json:"mac,omitempty"`
	Active   bool              `json:"active,omitempty"` // ip neigh REACHABLE：最近有通信
	Meta     map[string]string `json:"meta,omitempty"`
	LinkType string            `json:"link_type,omitempty"` // 钻取目标类型：vm / container / network
	LinkID   string            `json:"link_id,omitempty"`   // 钻取目标标识
}

// TopoEdge 拓扑边（当前仅桥接挂靠一种语义）。
// From/To 是 TopoNode.ID；Kind 固定 "attach"，表示「下层资源挂接在上层网络上」。
// 方向约定：host→nic / host→net / nic→net / net→vm / net→container，
// 前端按层次布局时可直接依赖这个方向。
type TopoEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// NetworkTopologyHandler 网络拓扑处理器。
//
// Virt/Docker 都是封装层实例（service/virt、service/dockerx），handler 不直接
// 调 libvirt/docker；两者均允许为 nil，调用前逐个判空，使「libvirt 不可用但
// Docker 可用」时仍能画出 Docker 侧拓扑。
type NetworkTopologyHandler struct {
	Virt   *virt.Virt
	Docker *dockerx.Dockerx
}

// NewNetworkTopologyHandler 创建网络拓扑处理器。
func NewNetworkTopologyHandler() *NetworkTopologyHandler {
	return &NetworkTopologyHandler{Virt: virt.New(), Docker: dockerx.New()}
}

// netCIDR 由网关 + 掩码合成网络 CIDR（libvirt 的 NetworkInfo.CIDR 实为 netmask 字符串）。
//
// 只处理 IPv4：平台网络与 Docker 默认网络均为 IPv4，To4() 同时承担「非法地址」
// 与「IPv6」的过滤，返回空串让调用方跳过该展示字段，而不是报错中断整张图。
func netCIDR(gateway, mask string) string {
	ip := net.ParseIP(gateway).To4()
	m := net.ParseIP(mask).To4()
	if ip == nil || m == nil {
		return ""
	}
	return (&net.IPNet{IP: ip.Mask(net.IPMask(m)), Mask: net.IPMask(m)}).String()
}

// shortID docker 短 id：与 docker ps 的 ID 列（前 12 位）对齐，供前端/A I 匹配容器。
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func countKind(nodes []TopoNode, kind string) int {
	n := 0
	for _, x := range nodes {
		if x.Kind == kind {
			n++
		}
	}
	return n
}

// Topology GET /api/networks/topology —— 宿主机 → 桥/虚拟网络 → VM/容器 三层关系图。
//
// 数据全部来自实时采集（libvirt / Docker / ip 命令），不落库、不缓存：拓扑是
// 「此刻机器长什么样」，缓存反而会展示过期连接。任一数据源失败只丢对应图层，
// 不返回 500——半张图仍比空白页有诊断价值（错误不向前端暴露内部细节）。
//
// 构图顺序：先放 host/物理网卡/libvirt 网络/Docker 网络，再补 nic→net 的桥接
// 边，最后挂 VM 与容器；这样 VM/容器的挂靠目标一定已经存在。
func (h *NetworkTopologyHandler) Topology(c *gin.Context) {
	nodes := []TopoNode{}
	edges := []TopoEdge{}
	// 小闭包：add 追加节点，link 追加一条 attach 边，避免每个分支重复 append 字面量
	add := func(n TopoNode) { nodes = append(nodes, n) }
	link := func(from, to string) { edges = append(edges, TopoEdge{From: from, To: to, Kind: "attach"}) }

	// 宿主机名取不到时用中文占位，保证图上永远有根节点
	hostname := "宿主机"
	if hn, err := os.Hostname(); err == nil && hn != "" {
		hostname = hn
	}
	add(TopoNode{ID: "host", Kind: "host", Name: hostname, Subtitle: "宿主机"})

	// 邻居表：IP → 是否 REACHABLE（「最近有通信」活跃标注；同桥 L2 流量本就是盲区，只做尽力而为）
	reachable := map[string]bool{}
	if ns, err := hostnet.Neighbors(); err == nil {
		for _, n := range ns {
			if n.State == "REACHABLE" {
				reachable[n.IP] = true
			}
		}
	}

	// 物理网卡（排除 lo 与虚拟接口）
	if nics, err := hostnet.Interfaces(); err == nil {
		for _, n := range nics {
			if n.Name == "lo" || !n.Physical {
				continue
			}
			id := "nic:" + n.Name
			add(TopoNode{ID: id, Kind: "nic", Name: n.Name, State: n.State, IPs: n.Addresses, Subtitle: "物理网卡"})
			link("host", id)
		}
	}

	// libvirt 虚拟网络
	bridgeToNet := map[string]string{} // 网桥名 → 节点 id（供物理口/直连桥的 VM 挂靠）
	if h.Virt != nil {
		if nets, err := h.Virt.ListNetworks(); err == nil {
			for _, n := range nets {
				id := "libvirt:" + n.Name
				// Meta 的键直接用中文：这是纯展示字段，前端原样渲染，不做键名映射
				m := map[string]string{}
				if n.Bridge != "" {
					m["网桥"] = n.Bridge
					bridgeToNet[n.Bridge] = id
				}
				if sub := netCIDR(n.Gateway, n.CIDR); sub != "" {
					m["网段"] = sub
				}
				if n.Gateway != "" {
					m["网关"] = n.Gateway
				}
				if n.Forward != "" {
					m["转发"] = n.Forward
				}
				if n.DhcpStart != "" {
					m["DHCP"] = n.DhcpStart + " - " + n.DhcpEnd
				}
				st := "停止"
				if n.Active {
					st = "运行"
				}
				add(TopoNode{ID: id, Kind: "libvirt_net", Name: n.Name, State: st, Subtitle: "虚拟网络", Meta: m, LinkType: "network", LinkID: n.Name})
				link("host", id)
			}
		}
	}

	// Docker 网络（Docker 未运行/无权限时静默跳过；一次 inspect 复用给容器挂接）
	var dockerNets []dockerx.NetworkTopo
	if h.Docker != nil {
		if nets, err := h.Docker.NetworkTopology(); err == nil {
			dockerNets = nets
			for _, n := range nets {
				id := "docker:" + n.Name
				m := map[string]string{}
				if n.Driver != "" {
					m["驱动"] = n.Driver
				}
				if n.Subnet != "" {
					m["网段"] = n.Subnet
				}
				if n.Gateway != "" {
					m["网关"] = n.Gateway
				}
				if n.Builtin {
					m["类型"] = "内置"
				}
				add(TopoNode{ID: id, Kind: "docker_net", Name: n.Name, State: "运行", Subtitle: "Docker 网络", Meta: m, LinkType: "network", LinkID: n.Name})
				link("host", id)
			}
		}
	}

	// 已上图网络节点集合（VM/容器挂靠边只连已知网络，避免悬空）
	netIDs := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == "libvirt_net" || n.Kind == "docker_net" {
			netIDs[n.ID] = true
		}
	}

	// 物理口经桥接上联（bridge link：物理口 master=网桥）：nic → net
	if links, err := hostnet.BridgeLinks(); err == nil {
		nicIDs := map[string]bool{}
		for _, n := range nodes {
			if n.Kind == "nic" {
				nicIDs[strings.TrimPrefix(n.ID, "nic:")] = true
			}
		}
		for _, l := range links {
			if !nicIDs[l.Slave] {
				continue
			}
			if netID, ok := bridgeToNet[l.Master]; ok {
				link("nic:"+l.Slave, netID)
			}
		}
	}

	// DHCP 租约：MAC → 实际 IP（VM 补 IP 用）。
	// 只覆盖由 libvirt dnsmasq 分配地址的域；静态 IP / 桥接直连的 VM 可能查不到，
	// 此时节点只是没有 IP 展示，不影响拓扑结构。
	leaseByMAC := map[string]string{}
	if h.Virt != nil {
		if leases, err := h.Virt.ListDHCPLeases(); err == nil {
			for _, l := range leases {
				if l.MAC != "" {
					leaseByMAC[strings.ToLower(l.MAC)] = l.IP
				}
			}
		}
	}

	// VM 节点 + 网络→VM 边
	if h.Virt != nil {
		if doms, err := h.Virt.ListAllDomainNetworks(); err == nil {
			// idx: vm 节点 id → 在 nodes 中的下标。一台 VM 可能有多块网卡，
			// 每块网卡都要连边，但节点只能出现一次，故用下标回写 IP/MAC。
			idx := map[string]int{}
			for _, d := range doms {
				hasNet := false
				for _, ifc := range d.Interfaces {
					if ifc.Source != "" {
						hasNet = true
						break
					}
				}
				if !hasNet {
					continue // 无网络挂接的 VM 不上图（没有边可画，只会成为孤立节点）
				}
				vmID := "vm:" + d.Name
				if _, ok := idx[vmID]; !ok {
					idx[vmID] = len(nodes)
					add(TopoNode{ID: vmID, Kind: "vm", Name: d.Name, State: d.State, Subtitle: "虚拟机", LinkType: "vm", LinkID: d.Name})
				}
				n := &nodes[idx[vmID]]
				for _, ifc := range d.Interfaces {
					if ifc.Source == "" {
						continue
					}
					// source 可能是网络名，也可能是桥名（bridge 直连域）
					tgt := "libvirt:" + ifc.Source
					if !netIDs[tgt] {
						bid, ok := bridgeToNet[ifc.Source]
						if !ok {
							continue // 引用未知网络，跳过该边
						}
						tgt = bid
					}
					ip := leaseByMAC[strings.ToLower(ifc.MAC)]
					if ip != "" && !hasString(n.IPs, ip) {
						n.IPs = append(n.IPs, ip)
					}
					if ip != "" && reachable[ip] {
						n.Active = true
					}
					if ifc.MAC != "" && n.MAC == "" {
						n.MAC = ifc.MAC
					}
					link(tgt, vmID)
				}
			}
		}
	}

	// 容器节点 + 网络→容器 边（挂接来自 network inspect，仅运行中/暂停容器有端点）
	{
		idx := map[string]int{}
		for _, n := range dockerNets {
			netID := "docker:" + n.Name
			if !netIDs[netID] {
				continue
			}
			for _, ct := range n.Containers {
				sid := shortID(ct.ID)
				cid := "container:" + sid
				if _, ok := idx[cid]; !ok {
					idx[cid] = len(nodes)
					add(TopoNode{ID: cid, Kind: "container", Name: ct.Name, Subtitle: "容器", LinkType: "container", LinkID: sid})
				}
				c := &nodes[idx[cid]]
				if ct.IPv4 != "" && !hasString(c.IPs, ct.IPv4) {
					c.IPs = append(c.IPs, ct.IPv4)
				}
				if ct.IPv4 != "" && reachable[ct.IPv4] {
					c.Active = true
				}
				if ct.MAC != "" && c.MAC == "" {
					c.MAC = ct.MAC
				}
				link(netID, cid)
			}
		}
	}

	Success(c, gin.H{
		"nodes":      nodes,
		"edges":      edges,
		"hostname":   hostname,
		"sampled_at": time.Now().Format("15:04:05"),
		"stats": gin.H{
			"vms":        countKind(nodes, "vm"),
			"containers": countKind(nodes, "container"),
			"networks":   countKind(nodes, "libvirt_net") + countKind(nodes, "docker_net"),
		},
	})
}
