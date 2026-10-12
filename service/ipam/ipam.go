// Package ipam 提供 IPv4 网段的「可用地址区间差集」纯算法。
//
// 设计参考 NetBox 的 Prefix.get_available_ips()/get_first_available_ip()：可用地址
// 不落库、每次现算——拿网段全集减去已占用集合，得出若干「连续空闲区间」（而不是逐
// IP 罗列；一个 /24 有 254 个地址，逐条既慢又没法看）。
//
// 为什么下沉成独立包（而非塞进 handler）：
//  1. 纯函数、零依赖（不 import gin/gorm/libvirt），边界（网段首尾、网关保留、跨越
//     已用段、全满、全空）可以穷举单测，不需要起 HTTP/DB；
//  2. handler 层只做「聚合三源数据 + 调本包」，保持薄；
//  3. 未来「下一个可用 IP」独立接口、建机前置校验可复用同一套口径。
//
// 取舍：只做 IPv4。平台现有数据源（libvirt NAT、docker 默认网络、vms.ip）全是
// IPv4；IPv6 的地址空间与保留口径差异大，等真有需求再单独建模，不在此处用「通用」
// 接口掩盖半成品。
package ipam

import (
	"net"
	"sort"
	"strings"
)

// Range 一段连续地址区间（含首尾）。Size = end - start + 1。
// 前端据此渲染「10.0.0.10 - 10.0.0.50（41 个空闲）」，而非逐 IP 列出。
type Range struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Size  int    `json:"size"`
}

// hostBounds 返回网段内「可用主机地址」的闭区间 [first, last]（用 uint64 便于
// 做 +1/-1 且不与 32 位地址空间边界撞溢出）。口径与 handler.usableCount 对齐：
//   - 前缀 ≤ /30：扣除网络地址与广播地址（first=network+1, last=broadcast-1）
//   - /31：RFC 3021，点对点两端地址均可用（first=network, last=network+1）
//   - /32：唯一地址即主机地址
//
// 非 IPv4 或掩码长度非 32 位返回 ok=false。
func hostBounds(ipnet *net.IPNet) (first, last uint64, ok bool) {
	if ipnet == nil {
		return 0, 0, false
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return 0, 0, false
	}
	ip4 := ipnet.IP.To4()
	if ip4 == nil {
		return 0, 0, false
	}
	mask := maskToU32(ipnet.Mask)
	network := ipToU32(ip4) & mask
	// broadcast = network | ^mask；/0 时 mask=0，结果恰为 0xFFFFFFFF，无需特判
	broadcast := network | ^mask

	switch {
	case ones <= 30:
		return uint64(network) + 1, uint64(broadcast) - 1, true
	case ones == 31:
		return uint64(network), uint64(network) + 1, true
	default: // /32
		return uint64(network), uint64(network), true
	}
}

// FreeRanges 计算网段内未被 occupied 占用的连续空闲区间（按地址升序）。
//
// occupied 应传「已分配对象 ∪ 保留地址（如网关）」的并集。网段内重复项自动去重；
// 越界、非法、IPv6 条目一律忽略（邻居表/租约里可能混入其它网段的地址）。
// 返回空切片表示无空闲（含全满）；ipnet 非法返回 nil。
func FreeRanges(ipnet *net.IPNet, occupied []string) []Range {
	first, last, ok := hostBounds(ipnet)
	if !ok {
		return nil
	}

	// 收集落在 [first,last] 内的占用地址并去重
	set := make(map[uint64]struct{}, len(occupied))
	for _, s := range occupied {
		v, vok := ipToU64(s)
		if !vok || v < first || v > last {
			continue
		}
		set[v] = struct{}{}
	}
	if len(set) == 0 {
		return []Range{{Start: u64ToIP(first), End: u64ToIP(last), Size: int(last - first + 1)}}
	}

	occ := make([]uint64, 0, len(set))
	for v := range set {
		occ = append(occ, v)
	}
	sort.Slice(occ, func(i, j int) bool { return occ[i] < occ[j] })

	out := make([]Range, 0, len(occ)+1)
	cur := first
	for _, v := range occ {
		if v > cur {
			out = append(out, mkRange(cur, v-1))
		}
		cur = v + 1 // uint64：v 最大 0xFFFFFFFF，+1 不溢出
	}
	if cur <= last {
		out = append(out, mkRange(cur, last))
	}
	return out
}

// FirstFree 返回网段内第一个空闲地址（对应 NetBox 的 get_first_available_ip）。
// 无空闲返回 ""。等价 FreeRanges(...) 首区间的起点。
func FirstFree(ipnet *net.IPNet, occupied []string) string {
	rs := FreeRanges(ipnet, occupied)
	if len(rs) == 0 {
		return ""
	}
	return rs[0].Start
}

// mkRange 由起止 uint64 构造区间（含 Size）。
func mkRange(start, end uint64) Range {
	return Range{Start: u64ToIP(start), End: u64ToIP(end), Size: int(end - start + 1)}
}

// ipToU32 取 IPv4 的 32 位数值；非 IPv4 返回 0（仅用于掩码等已知合法的输入）。
func ipToU32(ip net.IP) uint32 {
	v4 := ip.To4()
	if v4 == nil {
		return 0
	}
	return uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])
}

// maskToU32 将 4 字节掩码转 32 位数值（len<4 时高位补 0）。
func maskToU32(m net.IPMask) uint32 {
	var v uint32
	for i := 0; i < 4 && i < len(m); i++ {
		v = v<<8 | uint32(m[i])
	}
	return v
}

// ipToU64 解析 IPv4 字符串为 64 位数值；非法或非 IPv4 返回 ok=false。
func ipToU64(s string) (uint64, bool) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return 0, false
	}
	v4 := ip.To4()
	if v4 == nil {
		return 0, false
	}
	return uint64(v4[0])<<24 | uint64(v4[1])<<16 | uint64(v4[2])<<8 | uint64(v4[3]), true
}

// u64ToIP 32 位数值转点分十进制字符串。
func u64ToIP(v uint64) string {
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).String()
}
