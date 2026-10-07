// network_ipam.go：IP 地址分配一览（N2b）——每个网段的已用/可用 IP 与归属。
// 三个现成数据源合并去重：libvirt DHCP 租约（VM 实得 IP）、docker network inspect
// （容器 IP）、宿主邻居表 ip neigh（兜底「最近活跃」）。纯聚合，无新采集。
//
// GET /api/networks/ipam（operator+）
package handler

import (
	"net"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/hostnet"
	"github.com/jiuzhao/vmops/service/virt"
)

// IPAMAddress 网段内一个已用地址及其归属。
type IPAMAddress struct {
	IP     string `json:"ip"`
	Owner  string `json:"owner"`
	Source string `json:"source"` // dhcp / container / neighbor
}

// IPAMItem 一个网段的地址分配概况。
type IPAMItem struct {
	Name      string        `json:"name"`
	Kind      string        `json:"kind"` // libvirt / docker
	Subnet    string        `json:"subnet"`
	Gateway   string        `json:"gateway"`
	DhcpStart string        `json:"dhcp_start,omitempty"`
	DhcpEnd   string        `json:"dhcp_end,omitempty"`
	Total     int           `json:"total"` // 可用地址总数（扣网络地址与广播）
	Used      int           `json:"used"`
	Free      int           `json:"free"`
	Addresses []IPAMAddress `json:"addresses"`
}

// NetworkIPAMHandler IP 分配一览处理器。
type NetworkIPAMHandler struct {
	Virt   *virt.Virt
	Docker *dockerx.Dockerx
}

// NewNetworkIPAMHandler 创建 IPAM 处理器。
func NewNetworkIPAMHandler() *NetworkIPAMHandler {
	return &NetworkIPAMHandler{Virt: virt.New(), Docker: dockerx.New()}
}

// usableCount 网段可用地址数（扣网络地址与广播；/31、/32 按 RFC 3021 特殊处理）。
func usableCount(ipnet *net.IPNet) int {
	if ipnet == nil {
		return 0
	}
	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones
	if hostBits >= 31 {
		return 0 // 超大网段不显示总数（避免溢出），仅列已用
	}
	n := 1 << hostBits
	if n <= 2 {
		return n
	}
	return n - 2
}

// ipLess 按数值比较两个 IPv4 字符串，供地址排序。
func ipLess(a, b string) bool {
	ia, ib := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	if ia == nil || ib == nil {
		return a < b
	}
	for i := 0; i < 4; i++ {
		if ia[i] != ib[i] {
			return ia[i] < ib[i]
		}
	}
	return false
}

// IPAM GET /api/networks/ipam —— 全部网段的地址分配一览。
func (h *NetworkIPAMHandler) IPAM(c *gin.Context) {
	items := []IPAMItem{}

	// 邻居表兜底来源（IP 集合）
	neighborIPs := []string{}
	if ns, err := hostnet.Neighbors(); err == nil {
		for _, n := range ns {
			if n.IP != "" {
				neighborIPs = append(neighborIPs, n.IP)
			}
		}
	}
	// DHCP 租约：IP → 主机名
	leaseOwner := map[string]string{}
	if h.Virt != nil {
		if leases, err := h.Virt.ListDHCPLeases(); err == nil {
			for _, l := range leases {
				if l.IP != "" {
					leaseOwner[l.IP] = l.Hostname
				}
			}
		}
	}

	// libvirt 网段
	if h.Virt != nil {
		if nets, err := h.Virt.ListNetworks(); err == nil {
			for _, n := range nets {
				subnet := netCIDR(n.Gateway, n.CIDR)
				if subnet == "" {
					continue
				}
				_, ipnet, perr := net.ParseCIDR(subnet)
				if perr != nil {
					continue
				}
				it := IPAMItem{Name: n.Name, Kind: "libvirt", Subnet: subnet, Gateway: n.Gateway, DhcpStart: n.DhcpStart, DhcpEnd: n.DhcpEnd, Addresses: []IPAMAddress{}}
				seen := map[string]bool{}
				add := func(ip, owner, src string) {
					if ip == "" || seen[ip] || !ipnet.Contains(net.ParseIP(ip)) {
						return
					}
					seen[ip] = true
					it.Addresses = append(it.Addresses, IPAMAddress{IP: ip, Owner: owner, Source: src})
				}
				for ip, owner := range leaseOwner {
					if owner == "" {
						owner = "（租约未报主机名）"
					}
					add(ip, owner, "dhcp")
				}
				for _, ip := range neighborIPs {
					add(ip, "（邻居表）", "neighbor")
				}
				sort.Slice(it.Addresses, func(i, j int) bool { return ipLess(it.Addresses[i].IP, it.Addresses[j].IP) })
				it.Total = usableCount(ipnet)
				it.Used = len(it.Addresses)
				it.Free = it.Total - it.Used
				if it.Free < 0 {
					it.Free = 0
				}
				items = append(items, it)
			}
		}
	}

	// docker 网段
	if h.Docker != nil {
		if nets, err := h.Docker.NetworkTopology(); err == nil {
			for _, n := range nets {
				if n.Subnet == "" {
					continue
				}
				_, ipnet, perr := net.ParseCIDR(n.Subnet)
				if perr != nil {
					continue
				}
				it := IPAMItem{Name: n.Name, Kind: "docker", Subnet: n.Subnet, Gateway: n.Gateway, Addresses: []IPAMAddress{}}
				seen := map[string]bool{}
				for _, ct := range n.Containers {
					if ct.IPv4 == "" || !ipnet.Contains(net.ParseIP(ct.IPv4)) || seen[ct.IPv4] {
						continue
					}
					seen[ct.IPv4] = true
					it.Addresses = append(it.Addresses, IPAMAddress{IP: ct.IPv4, Owner: ct.Name, Source: "container"})
				}
				sort.Slice(it.Addresses, func(i, j int) bool { return ipLess(it.Addresses[i].IP, it.Addresses[j].IP) })
				it.Total = usableCount(ipnet)
				it.Used = len(it.Addresses)
				it.Free = it.Total - it.Used
				if it.Free < 0 {
					it.Free = 0
				}
				items = append(items, it)
			}
		}
	}

	Success(c, gin.H{"items": items, "total": len(items)})
}
