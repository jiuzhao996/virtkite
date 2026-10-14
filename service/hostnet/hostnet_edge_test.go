package hostnet

import (
	"reflect"
	"testing"
)

// TestParseInterfacesEdge 覆盖 `ip -br addr` 解析的边界行。
//
// 风险点：真实 `ip -br addr` 在接口密集或半截输出时会夹带空行、单字段残行、
// 带 `@ifN` 的 veth 口名；解析器必须逐行容错、跳过残缺行，并始终返回**非 nil**
// 切片（handler 直接 JSON 序列化，返回 nil 会变成 `null`，前端 .map 会崩）。
func TestParseInterfacesEdge(t *testing.T) {
	cases := []struct {
		name     string
		out      string
		physical func(string) bool
		want     []NIC
	}{
		{
			name: "空输出返回非 nil 空切片",
			out:  "",
			want: []NIC{},
		},
		{
			name: "空行与纯空白行被跳过",
			out:  "\n\n   \n",
			want: []NIC{},
		},
		{
			name: "单字段残行被跳过（len<2）",
			out:  "lo\neno1 UP 10.0.0.1/24",
			want: []NIC{{Name: "eno1", State: "UP", Addresses: []string{"10.0.0.1/24"}}},
		},
		{
			name:     "physical 为 nil 时不判物理设备（全部 false）",
			out:      "eth0 UP 10.0.0.1/24",
			physical: nil,
			want:     []NIC{{Name: "eth0", State: "UP", Addresses: []string{"10.0.0.1/24"}}},
		},
		{
			name: "名称首位为 @ 不剥离（仅在 i>0 处裁切）",
			out:  "@weird  UP  10.0.0.9/24",
			want: []NIC{{Name: "@weird", State: "UP", Addresses: []string{"10.0.0.9/24"}}},
		},
		{
			name: "多地址保留原始顺序",
			out:  "eth0 UP 10.0.0.1/24 fe80::1/64 192.168.1.5/24",
			want: []NIC{{Name: "eth0", State: "UP", Addresses: []string{"10.0.0.1/24", "fe80::1/64", "192.168.1.5/24"}}},
		},
		{
			name:     "physical 判定用的是剥离 @ 后的名字（否则 /sys 路径会错）",
			out:      "veth1234@if2 UP 172.17.0.2/16",
			physical: func(n string) bool { return n == "veth1234" },
			want:     []NIC{{Name: "veth1234", State: "UP", Addresses: []string{"172.17.0.2/16"}, Physical: true}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseInterfaces(c.out, c.physical)
			if got == nil {
				t.Fatalf("返回 nil 切片（JSON 会序列化成 null）")
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseInterfaces=%+v\nwant                 %+v", got, c.want)
			}
		})
	}
}

// TestParseNeighborsEdge 覆盖 `ip neigh` 解析的边界行。
//
// 风险点：邻居表里存在缺 lladdr 的条目（FAILED/INCOMPLETE），也可能被截断成不足
// 四列；扫描 `dev`/`lladdr` 的值时必须做右界检查，绝不能越界 panic（进程内解析，
// panic 会带走整个请求链）。MAC 统一转小写便于与 vms.mac_address 比对。
func TestParseNeighborsEdge(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []Neighbor
	}{
		{
			name: "空输出返回非 nil 空切片",
			out:  "",
			want: []Neighbor{},
		},
		{
			name: "不足四列的行被跳过",
			out:  "1.2.3.4 dev\n10.0.0.5 dev virbr0 REACHABLE",
			want: []Neighbor{{IP: "10.0.0.5", Dev: "virbr0", State: "REACHABLE"}},
		},
		{
			name: "MAC 统一转小写",
			out:  "10.0.0.5 dev virbr0 lladdr AA:BB:CC:DD:EE:FF REACHABLE",
			want: []Neighbor{{IP: "10.0.0.5", Dev: "virbr0", MAC: "aa:bb:cc:dd:ee:ff", State: "REACHABLE"}},
		},
		{
			name: "制表符/多空格混排也能切分",
			out:  "10.0.0.5\tdev\tvirbr0\tlladdr\t52:54:00:11:22:33\tSTALE",
			want: []Neighbor{{IP: "10.0.0.5", Dev: "virbr0", MAC: "52:54:00:11:22:33", State: "STALE"}},
		},
		{
			// 退化行：dev 落在行尾、没有取值。真实 ip neigh 不会这样输出，
			// 此处专测「右界检查」——不得 panic，Dev 留空、State 取末列。
			name: "dev 位于行尾无取值不越界 panic",
			out:  "10.0.0.5 lladdr aa:bb dev",
			want: []Neighbor{{IP: "10.0.0.5", MAC: "aa:bb", Dev: "", State: "dev"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseNeighbors(c.out)
			if got == nil {
				t.Fatalf("返回 nil 切片（JSON 会序列化成 null）")
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseNeighbors=%+v\nwant             %+v", got, c.want)
			}
		})
	}
}

// TestParseBridgeLinksEdge 覆盖 `bridge link` 解析的边界行。
//
// 风险点：`bridge link` 对**未挂桥**的口不会打印 master 段；解析器必须把
// Master 为空的行过滤掉（否则会在拓扑里凭空画出无主从属边）。同时 slave 名
// 可能带 `: ` 与 `@ifN` 后缀，需一并裁掉。空输出必须返回非 nil 切片。
func TestParseBridgeLinksEdge(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []BridgeLink
	}{
		{
			name: "空输出返回非 nil 空切片",
			out:  "",
			want: []BridgeLink{},
		},
		{
			name: "无 master 段的行被过滤",
			out:  "5: veth1: <BROADCAST,MULTICAST,UP> mtu 1500 state forwarding",
			want: []BridgeLink{},
		},
		{
			name: "不足三列的行被跳过（仅保留下面的合法行）",
			out:  "5: veth1\n7: vnet0: <UP> master virbr0 state forwarding",
			want: []BridgeLink{{Slave: "vnet0", Master: "virbr0"}},
		},
		{
			name: "slave 名剥离 @ifN 与末尾冒号",
			out:  "5: veth1234@if2: <UP> master docker0 state forwarding",
			want: []BridgeLink{{Slave: "veth1234", Master: "docker0"}},
		},
		{
			// master 落在行尾、没有取值：循环上界 len(f)-1 恰好把它排除，
			// 专测不越界且被过滤。
			name: "master 位于行尾无取值不越界且被过滤",
			out:  "5: veth1: <UP> master",
			want: []BridgeLink{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseBridgeLinks(c.out)
			if got == nil {
				t.Fatalf("返回 nil 切片（JSON 会序列化成 null）")
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseBridgeLinks=%+v\nwant                %+v", got, c.want)
			}
		})
	}
}
