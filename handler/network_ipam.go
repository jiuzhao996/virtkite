// network_ipam.go：IP 地址分配一览（N2b）——每个网段的已用/可用 IP 与归属。
//
// 设计参考 NetBox 的 Prefix 详情页：
//   - 「可用 IP」不落库，每次用「网段全集 − 已占用集合」现算成若干连续空闲区间
//     （见 service/ipam）。一个 /24 有 254 个地址，逐条罗列既慢又没法看，区间表达
//     才能一眼看出「10.0.0.10 - 10.0.0.50 这一段是空的」。
//   - 前端把「已用行」与「空闲区间行」按地址交错排成一张表（NetBox 的
//     AvailableIPSpace 伪记录思路）。
//
// 数据源合并去重（优先级从高到低，先到先得）：
//  1. libvirt DHCP 租约（VM 实得 IP；按 MAC 认领到具体 VM 名与跳转 id）
//  2. vms.ip（静态 IP——静态地址永不出现在租约里，这是唯一自动来源）
//  3. docker network inspect（容器 IP）
//  4. 宿主邻居表 ip neigh（兜底「最近活跃」，同桥 L2 是盲区，尽力而为）
//
// 保留地址（网关）作为一行「reserved」列出并计入已占，让交错表能解释「网关为何不在
// 空闲区间里」；网络地址与广播地址由 service/ipam 的边界口径直接排除。
//
// 取舍：IP 归属暂不拆独立「接口/地址」表——单网卡 VM 已够用。将来支持多网卡时，
// 归属应改为按 (VM, 网卡 MAC, IP) 建模，届时把本文件的 vmByIP 索引换成接口级索引即可。
//
// GET /api/networks/ipam（operator+）
package handler

import (
	"log"
	"net"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/database"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/hostnet"
	"github.com/jiuzhao/vmops/service/ipam"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// IPAMAddress 网段内一个已用地址及其归属。
type IPAMAddress struct {
	IP       string `json:"ip"`
	Owner    string `json:"owner"`
	Source   string `json:"source"`              // dhcp / vm / container / neighbor / reserved
	VMID     uint   `json:"vm_id,omitempty"`     // 归属虚拟机 ID（可跳转）；非 VM 来源为 0
	VMStatus string `json:"vm_status,omitempty"` // 归属虚拟机状态（running/shut off/…）；非 VM 来源为空
}

// IPAMItem 一个网段的地址分配概况。
type IPAMItem struct {
	Name       string        `json:"name"`
	Kind       string        `json:"kind"` // libvirt / docker
	Subnet     string        `json:"subnet"`
	Gateway    string        `json:"gateway"`
	DhcpStart  string        `json:"dhcp_start,omitempty"`
	DhcpEnd    string        `json:"dhcp_end,omitempty"`
	Total      int           `json:"total"`               // 可用地址总数（扣网络地址与广播）
	Used       int           `json:"used"`                // 已占地址数（含网关保留行）
	Free       int           `json:"free"`                // 空闲地址数（= 各空闲区间之和）
	NextFree   string        `json:"next_free,omitempty"` // 下一个可用 IP（无空闲为空）
	Addresses  []IPAMAddress `json:"addresses"`           // 已用地址（升序）
	FreeRanges []ipam.Range  `json:"free_ranges"`         // 连续空闲区间（升序）
}

// NetworkIPAMHandler IP 分配一览处理器。
type NetworkIPAMHandler struct {
	Virt   *virt.Virt
	Docker *dockerx.Dockerx
	DB     *gorm.DB
}

// NewNetworkIPAMHandler 创建 IPAM 处理器。
// db 可选：传入则用于读取 vms.ip（静态 IP 归属）；不传回落到进程级 DB。
func NewNetworkIPAMHandler(db ...*gorm.DB) *NetworkIPAMHandler {
	h := &NetworkIPAMHandler{Virt: virt.New(), Docker: dockerx.New()}
	switch {
	case len(db) > 0 && db[0] != nil:
		h.DB = db[0]
	default:
		// 本批次 routes.go 未接线（禁止改动该文件），回落到 main 启动时初始化的进程级 DB。
		// 一旦 routes.go 改为 NewNetworkIPAMHandler(deps.DB)，此回落分支可删。
		h.DB = database.GetDB()
	}
	return h
}

// vmRef 归属虚拟机摘要（把租约/邻居/静态 IP 认领到具体 VM）。
type vmRef struct {
	id     uint
	name   string
	status string
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

// sumFree 空闲区间大小之和。
func sumFree(rs []ipam.Range) int {
	n := 0
	for _, r := range rs {
		n += r.Size
	}
	return n
}

// loadVMIndex 载入 VM 归属索引：MAC → VM（租约/邻居认领）、IP → VM（静态 IP 归属）。
// DB 不可用/查询失败时返回空索引，IPAM 仍能出图（只是归属显示退化为租约主机名）。
func (h *NetworkIPAMHandler) loadVMIndex() (byMAC, byIP map[string]vmRef) {
	byMAC = map[string]vmRef{}
	byIP = map[string]vmRef{}
	if h.DB == nil {
		return byMAC, byIP
	}
	var vms []model.VM
	// 只取归属所需列，避免把几 KB 的 DomainXML 也拉出来
	if err := h.DB.Select("id", "name", "mac_address", "ip", "status").Find(&vms).Error; err != nil {
		log.Printf("[ipam] 查询虚拟机失败（vms.ip 归属缺失）: %v", err)
		return byMAC, byIP
	}
	for i := range vms {
		ref := vmRef{id: vms[i].ID, name: vms[i].Name, status: vms[i].Status}
		if vms[i].MACAddress != "" {
			byMAC[strings.ToLower(vms[i].MACAddress)] = ref
		}
		if vms[i].IP != "" {
			byIP[vms[i].IP] = ref
		}
	}
	return byMAC, byIP
}

// IPAM GET /api/networks/ipam —— 全部网段的地址分配一览。
func (h *NetworkIPAMHandler) IPAM(c *gin.Context) {
	items := []IPAMItem{}

	vmByMAC, vmByIP := h.loadVMIndex()

	// DHCP 租约：按 MAC 认领到 VM，否则用租约主机名
	type leaseRec struct {
		ip, owner string
		ref       *vmRef
	}
	leaseRecs := []leaseRec{}
	if h.Virt != nil {
		if leases, err := h.Virt.ListDHCPLeases(); err == nil {
			for _, l := range leases {
				if l.IP == "" {
					continue
				}
				owner := l.Hostname
				var ref *vmRef
				if r, ok := vmByMAC[strings.ToLower(l.MAC)]; ok {
					owner, ref = r.name, &r
				} else if owner == "" {
					owner = "（租约未报主机名）"
				}
				leaseRecs = append(leaseRecs, leaseRec{ip: l.IP, owner: owner, ref: ref})
			}
		}
	}

	// 邻居表兜底来源（IP + MAC，MAC 命中 VM 则用 VM 名）
	type neighborRec struct{ ip, mac string }
	neighborRecs := []neighborRec{}
	if ns, err := hostnet.Neighbors(); err == nil {
		for _, n := range ns {
			if n.IP != "" {
				neighborRecs = append(neighborRecs, neighborRec{ip: n.IP, mac: n.MAC})
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
				add := func(ip, owner, src string, ref *vmRef) {
					if ip == "" || seen[ip] || !ipnet.Contains(net.ParseIP(ip)) {
						return
					}
					seen[ip] = true
					addr := IPAMAddress{IP: ip, Owner: owner, Source: src}
					if ref != nil {
						addr.VMID, addr.VMStatus = ref.id, ref.status
					}
					it.Addresses = append(it.Addresses, addr)
				}
				// 优先级即调用顺序（先到先得）：租约 > 静态 IP > 邻居
				for _, l := range leaseRecs {
					add(l.ip, l.owner, "dhcp", l.ref)
				}
				for ip, r := range vmByIP {
					ref := r
					add(ip, r.name, "vm", &ref)
				}
				for _, nb := range neighborRecs {
					owner := "（邻居表）"
					var ref *vmRef
					if r, ok := vmByMAC[strings.ToLower(nb.mac)]; ok {
						owner, ref = r.name, &r
					}
					add(nb.ip, owner, "neighbor", ref)
				}
				// 网关保留行：让交错表能解释「网关为何不在空闲区间里」
				if gw := net.ParseIP(n.Gateway); gw != nil && ipnet.Contains(gw) {
					add(n.Gateway, "网关（保留地址）", "reserved", nil)
				}
				finalizeItem(&it, ipnet)
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
				add := func(ip, owner, src string) {
					if ip == "" || seen[ip] || !ipnet.Contains(net.ParseIP(ip)) {
						return
					}
					seen[ip] = true
					it.Addresses = append(it.Addresses, IPAMAddress{IP: ip, Owner: owner, Source: src})
				}
				for _, ct := range n.Containers {
					add(ct.IPv4, ct.Name, "container")
				}
				if gw := net.ParseIP(n.Gateway); gw != nil && ipnet.Contains(gw) {
					add(n.Gateway, "网关（保留地址）", "reserved")
				}
				finalizeItem(&it, ipnet)
				items = append(items, it)
			}
		}
	}

	Success(c, gin.H{"items": items, "total": len(items)})
}

// finalizeItem 收尾：排序地址、现算空闲区间与下一个可用 IP、汇总计数。
// Used 含网关保留行，故 Used + Free == Total（网络地址/广播已由 ipam 边界口径排除）。
func finalizeItem(it *IPAMItem, ipnet *net.IPNet) {
	sort.Slice(it.Addresses, func(i, j int) bool { return ipLess(it.Addresses[i].IP, it.Addresses[j].IP) })
	occupied := make([]string, 0, len(it.Addresses))
	for _, a := range it.Addresses {
		occupied = append(occupied, a.IP)
	}
	it.FreeRanges = ipam.FreeRanges(ipnet, occupied)
	it.NextFree = ipam.FirstFree(ipnet, occupied)
	it.Total = usableCount(ipnet)
	it.Used = len(it.Addresses)
	it.Free = sumFree(it.FreeRanges)
}
