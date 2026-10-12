package ipam

import (
	"net"
	"reflect"
	"testing"
)

// mustCIDR 解析 CIDR，失败即 Fatal。
func mustCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", cidr, err)
	}
	return ipnet
}

// TestFreeRanges 覆盖差集算法的主要边界：全空、网关保留、跨越已用段、
// 网段首尾占用、全满、越界忽略、重复去重、/31 与 /32 特例。
func TestFreeRanges(t *testing.T) {
	cases := []struct {
		name     string
		cidr     string
		occupied []string
		want     []Range
	}{
		{
			name: "全空 /24：整段一个区间（扣网络地址与广播）",
			cidr: "10.0.0.0/24",
			want: []Range{{Start: "10.0.0.1", End: "10.0.0.254", Size: 254}},
		},
		{
			name:     "网关保留：.1 被占，空闲从 .2 起",
			cidr:     "10.0.0.0/24",
			occupied: []string{"10.0.0.1"},
			want:     []Range{{Start: "10.0.0.2", End: "10.0.0.254", Size: 253}},
		},
		{
			name:     "跨越已用段：中间三个已用把网段切成两段",
			cidr:     "10.0.0.0/24",
			occupied: []string{"10.0.0.10", "10.0.0.11", "10.0.0.12"},
			want: []Range{
				{Start: "10.0.0.1", End: "10.0.0.9", Size: 9},
				{Start: "10.0.0.13", End: "10.0.0.254", Size: 242},
			},
		},
		{
			name:     "网段首尾都被占：空闲只剩中间",
			cidr:     "10.0.0.0/29", // 主机 .1-.6
			occupied: []string{"10.0.0.1", "10.0.0.6"},
			want:     []Range{{Start: "10.0.0.2", End: "10.0.0.5", Size: 4}},
		},
		{
			name:     "全满：无空闲返回空切片",
			cidr:     "10.0.0.0/30", // 主机 .1-.2
			occupied: []string{"10.0.0.1", "10.0.0.2"},
			want:     []Range{},
		},
		{
			name:     "越界/非法条目忽略（邻居表可能混入其它网段）",
			cidr:     "10.0.0.0/30",
			occupied: []string{"192.168.1.1", "not-an-ip", "10.0.0.5", ""},
			want:     []Range{{Start: "10.0.0.1", End: "10.0.0.2", Size: 2}},
		},
		{
			name:     "重复条目去重",
			cidr:     "10.0.0.0/30",
			occupied: []string{"10.0.0.1", "10.0.0.1"},
			want:     []Range{{Start: "10.0.0.2", End: "10.0.0.2", Size: 1}},
		},
		{
			name:     "/31 RFC 3021：两端均可用，占一端后剩另一端",
			cidr:     "10.0.0.0/31",
			occupied: []string{"10.0.0.0"},
			want:     []Range{{Start: "10.0.0.1", End: "10.0.0.1", Size: 1}},
		},
		{
			name: "/32 单地址：未占即空闲",
			cidr: "10.0.0.5/32",
			want: []Range{{Start: "10.0.0.5", End: "10.0.0.5", Size: 1}},
		},
		{
			name:     "/32 单地址：被占则空",
			cidr:     "10.0.0.5/32",
			occupied: []string{"10.0.0.5"},
			want:     []Range{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FreeRanges(mustCIDR(t, c.cidr), c.occupied)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("FreeRanges(%s, %v)=\n  %+v\nwant\n  %+v", c.cidr, c.occupied, got, c.want)
			}
		})
	}
}

// TestFreeRangesBadInput 非法网段（nil / IPv6）返回 nil。
func TestFreeRangesBadInput(t *testing.T) {
	if got := FreeRanges(nil, nil); got != nil {
		t.Errorf("FreeRanges(nil)=%v want nil", got)
	}
	if got := FreeRanges(mustCIDR(t, "2001:db8::/64"), nil); got != nil {
		t.Errorf("FreeRanges(ipv6)=%v want nil", got)
	}
}

// TestFirstFree 覆盖「下一个可用 IP」：取首区间起点；全满返回空串。
func TestFirstFree(t *testing.T) {
	cases := []struct {
		name     string
		cidr     string
		occupied []string
		want     string
	}{
		{"空网段取首主机地址", "10.0.0.0/24", nil, "10.0.0.1"},
		{"网关被占则跳过 .1", "10.0.0.0/24", []string{"10.0.0.1"}, "10.0.0.2"},
		{"连续已用段之后取下一跳", "10.0.0.0/24", []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, "10.0.0.4"},
		{"全满返回空串", "10.0.0.0/30", []string{"10.0.0.1", "10.0.0.2"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FirstFree(mustCIDR(t, c.cidr), c.occupied); got != c.want {
				t.Errorf("FirstFree(%s, %v)=%q want %q", c.cidr, c.occupied, got, c.want)
			}
		})
	}
}

// TestHostBounds 校验主机地址边界口径与 usableCount 一致。
func TestHostBounds(t *testing.T) {
	cases := []struct {
		cidr        string
		first, last uint64
	}{
		{"10.0.0.0/24", u32v("10.0.0.1"), u32v("10.0.0.254")},
		{"10.0.0.0/30", u32v("10.0.0.1"), u32v("10.0.0.2")},
		{"10.0.0.0/31", u32v("10.0.0.0"), u32v("10.0.0.1")},
		{"10.0.0.5/32", u32v("10.0.0.5"), u32v("10.0.0.5")},
	}
	for _, c := range cases {
		f, l, ok := hostBounds(mustCIDR(t, c.cidr))
		if !ok {
			t.Fatalf("hostBounds(%s) ok=false", c.cidr)
		}
		if f != c.first || l != c.last {
			t.Errorf("hostBounds(%s)=[%d,%d] want [%d,%d]", c.cidr, f, l, c.first, c.last)
		}
	}
}

// u32v 测试辅助：IPv4 字符串 → uint64 数值。
func u32v(s string) uint64 {
	v, _ := ipToU64(s)
	return v
}
