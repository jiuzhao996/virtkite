package handler

import (
	"net"
	"testing"
)

func TestUsableCount(t *testing.T) {
	cases := []struct {
		cidr string
		want int
	}{
		{"10.0.0.0/24", 254},
		{"192.168.0.0/16", 65534},
		{"172.17.0.0/28", 14},
		{"10.0.0.0/30", 2},
		{"10.0.0.0/31", 2}, // RFC 3021：点对点两地址均可用
		{"10.0.0.1/32", 1},
		{"10.0.0.0/8", 16777214}, // 大网段照实算（64 位 int 无溢出）
		{"10.0.0.0/1", 0},        // /1 及更短：hostBits>=31 保护分支
	}
	for _, c := range cases {
		_, ipnet, err := net.ParseCIDR(c.cidr)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", c.cidr, err)
		}
		if got := usableCount(ipnet); got != c.want {
			t.Errorf("usableCount(%s)=%d want %d", c.cidr, got, c.want)
		}
	}
	if got := usableCount(nil); got != 0 {
		t.Errorf("usableCount(nil)=%d want 0", got)
	}
}

func TestIPLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"10.0.0.2", "10.0.0.10", true},  // 数值比较而非字典序
		{"10.0.0.10", "10.0.0.2", false},
		{"10.0.0.1", "10.0.0.1", false},
		{"172.17.0.2", "172.17.0.3", true},
		{"192.168.1.1", "10.0.0.1", false},
	}
	for _, c := range cases {
		if got := ipLess(c.a, c.b); got != c.want {
			t.Errorf("ipLess(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}
