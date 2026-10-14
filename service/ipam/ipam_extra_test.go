package ipam

import (
	"net"
	"reflect"
	"testing"
)

// TestIPToU64 校验 IPv4 字符串 → 64 位数值的解析口径。
//
// 风险点：占用集合来自异构数据源（vms.ip、libvirt 租约、docker 网络），可能夹带
// 空白、IPv6、垃圾串。非 IPv4 必须返回 ok=false 被 FreeRanges 忽略，绝不能当成
// 0.0.0.0 参与差集（会把合法占用当成 .0 从而误判空闲）。
func TestIPToU64(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"10.0.0.1", u32v("10.0.0.1"), true},
		{" 10.0.0.1 ", u32v("10.0.0.1"), true}, // 前后空白被 TrimSpace
		{"0.0.0.0", 0, true},
		{"255.255.255.255", 0xFFFFFFFF, true},
		{"::ffff:10.0.0.1", u32v("10.0.0.1"), true}, // IPv4-mapped IPv6 归一化为 v4（Go To4 语义）
		{"2001:db8::1", 0, false},                   // 纯 IPv6 忽略
		{"not-an-ip", 0, false},
		{"", 0, false},
		{"1.2.3.4.5", 0, false},
	}
	for _, c := range cases {
		got, ok := ipToU64(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ipToU64(%q)=(%d,%v) want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestU64ToIP 校验数值 → 点分十进制，并做一次往返一致性。
func TestU64ToIP(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0.0.0.0"},
		{0x01020304, "1.2.3.4"},
		{0xFFFFFFFF, "255.255.255.255"},
		{u32v("10.0.0.1"), "10.0.0.1"},
	}
	for _, c := range cases {
		if got := u64ToIP(c.in); got != c.want {
			t.Errorf("u64ToIP(%d)=%q want %q", c.in, got, c.want)
		}
	}
	// 往返：ipToU64 → u64ToIP 应还原
	if s := u64ToIP(mustIPToU64(t, "192.168.31.156")); s != "192.168.31.156" {
		t.Errorf("往返不一致: %q", s)
	}
}

// TestIPToU32 覆盖 ipToU32：IPv4 取 32 位值，非 IPv4 返回 0。
func TestIPToU32(t *testing.T) {
	if got := ipToU32(net.ParseIP("10.0.0.1")); got != 0x0A000001 {
		t.Errorf("ipToU32(10.0.0.1)=%#x want 0x0a000001", got)
	}
	if got := ipToU32(net.ParseIP("2001:db8::1")); got != 0 {
		t.Errorf("ipToU32(IPv6)=%#x want 0", got)
	}
}

// TestMaskToU32 覆盖 4 字节掩码 → 32 位数值（高位在前，不足补 0）。
//
// 风险点：hostBounds 用 mask 推导网络地址与广播地址，掩码换算错位会让整段
// 地址范围偏移（如 /24 变成 /16）。
func TestMaskToU32(t *testing.T) {
	cases := []struct {
		ones int
		want uint32
	}{
		{0, 0x00000000},
		{24, 0xFFFFFF00},
		{30, 0xFFFFFFFC},
		{31, 0xFFFFFFFE},
		{32, 0xFFFFFFFF},
	}
	for _, c := range cases {
		if got := maskToU32(net.CIDRMask(c.ones, 32)); got != c.want {
			t.Errorf("maskToU32(/%d)=%#x want %#x", c.ones, got, c.want)
		}
	}
	// 空掩码视为全 0（len<4 补 0）
	if got := maskToU32(net.IPMask{}); got != 0 {
		t.Errorf("maskToU32(empty)=%#x want 0", got)
	}
}

// TestHostBoundsBadInput 校验 hostBounds 对非法输入的否决：nil、IPv6、非 IPv4。
func TestHostBoundsBadInput(t *testing.T) {
	if _, _, ok := hostBounds(nil); ok {
		t.Error("hostBounds(nil) 应 ok=false")
	}
	if _, _, ok := hostBounds(mustCIDR(t, "2001:db8::/64")); ok {
		t.Error("hostBounds(IPv6) 应 ok=false")
	}
}

// TestHostBoundsZeroPrefix 覆盖 /0 极端边界：整段 IPv4 空间。
//
// 风险点：代码注释断言 「/0 时 mask=0，broadcast=0xFFFFFFFF 无需特判」。此用例把该
// 断言钉死，防止将来有人给 broadcast 计算加 mask==0 特判时回归。
func TestHostBoundsZeroPrefix(t *testing.T) {
	f, l, ok := hostBounds(mustCIDR(t, "0.0.0.0/0"))
	if !ok {
		t.Fatal("hostBounds(0.0.0.0/0) ok=false")
	}
	if f != u32v("0.0.0.1") || l != u32v("255.255.255.254") {
		t.Errorf("hostBounds(/0)=[%d,%d] want [0.0.0.1, 255.255.255.254]", f, l)
	}
}

// TestFreeRangesMore 补充差集算法边界：/0 全域、网络/广播地址占用被忽略、
// 乱序占用、单点切分、IPv4-mapped 占用、/31 全满与半满。
func TestFreeRangesMore(t *testing.T) {
	cases := []struct {
		name     string
		cidr     string
		occupied []string
		want     []Range
	}{
		{
			name: "/0 全域：主机区间 [0.0.0.1, 255.255.255.254]",
			cidr: "0.0.0.0/0",
			want: []Range{{Start: "0.0.0.1", End: "255.255.255.254", Size: 4294967294}},
		},
		{
			// 网络地址 .0 与广播地址 .255 不在主机区间 [.1,.254] 内，属越界条目应被忽略
			name:     "网络地址与广播地址的占用被忽略",
			cidr:     "10.0.0.0/24",
			occupied: []string{"10.0.0.0", "10.0.0.255"},
			want:     []Range{{Start: "10.0.0.1", End: "10.0.0.254", Size: 254}},
		},
		{
			name:     "乱序占用按地址升序产出区间",
			cidr:     "10.0.0.0/24",
			occupied: []string{"10.0.0.50", "10.0.0.10"},
			want: []Range{
				{Start: "10.0.0.1", End: "10.0.0.9", Size: 9},
				{Start: "10.0.0.11", End: "10.0.0.49", Size: 39},
				{Start: "10.0.0.51", End: "10.0.0.254", Size: 204},
			},
		},
		{
			name:     "单个中点占用把网段切成两段",
			cidr:     "10.0.0.0/24",
			occupied: []string{"10.0.0.128"},
			want: []Range{
				{Start: "10.0.0.1", End: "10.0.0.127", Size: 127},
				{Start: "10.0.0.129", End: "10.0.0.254", Size: 126},
			},
		},
		{
			// IPv4-mapped 写法经 To4 归一化为 10.0.0.1，同样算占用
			name:     "IPv4-mapped 占用计数",
			cidr:     "10.0.0.0/30",
			occupied: []string{"::ffff:10.0.0.1"},
			want:     []Range{{Start: "10.0.0.2", End: "10.0.0.2", Size: 1}},
		},
		{
			name:     "/31 只占低端：高端空闲",
			cidr:     "10.0.0.0/31",
			occupied: []string{"10.0.0.1"},
			want:     []Range{{Start: "10.0.0.0", End: "10.0.0.0", Size: 1}},
		},
		{
			name:     "/31 两端全占：无空闲",
			cidr:     "10.0.0.0/31",
			occupied: []string{"10.0.0.0", "10.0.0.1"},
			want:     []Range{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FreeRanges(mustCIDR(t, c.cidr), c.occupied)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("FreeRanges(%s,%v)=\n  %+v\nwant\n  %+v", c.cidr, c.occupied, got, c.want)
			}
		})
	}
}

// TestFirstFreeMore 补充 FirstFree 的非法输入否决。
func TestFirstFreeMore(t *testing.T) {
	if got := FirstFree(nil, nil); got != "" {
		t.Errorf("FirstFree(nil)=%q want \"\"", got)
	}
	if got := FirstFree(mustCIDR(t, "2001:db8::/64"), nil); got != "" {
		t.Errorf("FirstFree(IPv6)=%q want \"\"", got)
	}
	if got := FirstFree(mustCIDR(t, "0.0.0.0/0"), nil); got != "0.0.0.1" {
		t.Errorf("FirstFree(/0)=%q want 0.0.0.1", got)
	}
}

// mustIPToU64 测试辅助：解析 IPv4 字符串为 uint64，失败即 Fatal。
func mustIPToU64(t *testing.T, s string) uint64 {
	t.Helper()
	v, ok := ipToU64(s)
	if !ok {
		t.Fatalf("ipToU64(%q) 失败", s)
	}
	return v
}
