package handler

import "testing"

func TestNetCIDR(t *testing.T) {
	cases := []struct{ gw, mask, want string }{
		{"10.0.0.1", "255.255.255.0", "10.0.0.0/24"},
		{"192.168.100.1", "255.255.0.0", "192.168.0.0/16"},
		{"172.17.0.1", "255.255.255.240", "172.17.0.0/28"},
		{"", "255.255.255.0", ""}, // 网关缺失
		{"10.0.0.1", "", ""},      // 掩码缺失（CIDR 字段实为 netmask）
		{"not-an-ip", "255.0.0.0", ""},
	}
	for _, c := range cases {
		if got := netCIDR(c.gw, c.mask); got != c.want {
			t.Errorf("netCIDR(%q,%q)=%q want %q", c.gw, c.mask, got, c.want)
		}
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("5df500b26c94abcdef0011"); got != "5df500b26c94" {
		t.Errorf("超长 id 应截前 12 位，got %q", got)
	}
	if got := shortID("abc"); got != "abc" {
		t.Errorf("短 id 原样返回，got %q", got)
	}
}

func TestHasStringAndCountKind(t *testing.T) {
	if !hasString([]string{"a", "b"}, "b") || hasString([]string{"a"}, "c") {
		t.Error("hasString 判定异常")
	}
	nodes := []TopoNode{{Kind: "vm"}, {Kind: "vm"}, {Kind: "container"}}
	if countKind(nodes, "vm") != 2 || countKind(nodes, "container") != 1 || countKind(nodes, "host") != 0 {
		t.Error("countKind 计数异常")
	}
}
