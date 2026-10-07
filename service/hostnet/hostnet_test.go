package hostnet

import "testing"

func TestParseInterfaces(t *testing.T) {
	out := `lo               UNKNOWN        127.0.0.1/8 ::1/128
eno1             DOWN
wlp4s0           UP             192.168.31.156/24 fe80::1/64
virbr0           UP             10.0.0.1/24
veth1234@if2     UP             172.17.0.2/16`
	got := parseInterfaces(out, func(name string) bool { return name == "wlp4s0" || name == "eno1" })
	if len(got) != 5 {
		t.Fatalf("期望 5 个接口，得到 %d: %+v", len(got), got)
	}
	if got[0].Name != "lo" || got[0].State != "UNKNOWN" || len(got[0].Addresses) != 2 {
		t.Errorf("lo 解析异常: %+v", got[0])
	}
	if got[1].Name != "eno1" || got[1].State != "DOWN" || len(got[1].Addresses) != 0 || !got[1].Physical {
		t.Errorf("eno1 解析异常: %+v", got[1])
	}
	if !got[2].Physical || got[2].Addresses[0] != "192.168.31.156/24" {
		t.Errorf("wlp4s0 解析异常: %+v", got[2])
	}
	if got[3].Physical {
		t.Errorf("virbr0 不应判为物理口: %+v", got[3])
	}
	// veth@if2 去 @ 后缀
	if got[4].Name != "veth1234" {
		t.Errorf("veth @ 后缀未剥离: %+v", got[4])
	}
}

func TestParseNeighbors(t *testing.T) {
	out := `192.168.31.1 dev wlp4s0 lladdr aa:bb:cc:dd:ee:ff REACHABLE
10.0.0.5 dev virbr0 lladdr 52:54:00:11:22:33 STALE
172.17.0.2 dev docker0 FAILED`
	got := parseNeighbors(out)
	if len(got) != 3 {
		t.Fatalf("期望 3 条，得到 %d", len(got))
	}
	if got[0].IP != "192.168.31.1" || got[0].Dev != "wlp4s0" || got[0].MAC != "aa:bb:cc:dd:ee:ff" || got[0].State != "REACHABLE" {
		t.Errorf("首条解析异常: %+v", got[0])
	}
	// 无 lladdr 也可解析（FAILED 行）
	if got[2].State != "FAILED" || got[2].MAC != "" {
		t.Errorf("FAILED 行解析异常: %+v", got[2])
	}
}

func TestParseBridgeLinks(t *testing.T) {
	out := `5: veth1234@if2: <BROADCAST,MULTICAST,UP> mtu 1500 master docker0 state forwarding priority 32 cost 2
7: vnet0: <BROADCAST,MULTICAST,UP> mtu 1500 master virbr0 state forwarding`
	got := parseBridgeLinks(out)
	if len(got) != 2 {
		t.Fatalf("期望 2 条，得到 %d: %+v", len(got), got)
	}
	if got[0].Slave != "veth1234" || got[0].Master != "docker0" {
		t.Errorf("veth 解析异常: %+v", got[0])
	}
	if got[1].Slave != "vnet0" || got[1].Master != "virbr0" {
		t.Errorf("vnet 解析异常: %+v", got[1])
	}
}
