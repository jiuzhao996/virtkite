package virt

import (
	"encoding/xml"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// bridgeNameFormat 网桥名固定形态：virbr + 十进制数字，由 hashNetwork 从网络名算出。
// 外部输入无法影响这个形态，注入专项会用它证明 bridge 属性没被撬开。
var bridgeNameFormat = regexp.MustCompile(`^virbr[0-9]+$`)

// parsedNetworkXML 用于反解 NetworkXMLFromParams 的输出。
// 每个可能被注入的位置都是切片，方便数个数。
type parsedNetworkXML struct {
	XMLName  xml.Name `xml:"network"`
	Names    []string `xml:"name"`
	Forwards []struct {
		Mode string `xml:"mode,attr"`
		NATs []struct {
			Ports []struct {
				Start string `xml:"start,attr"`
				End   string `xml:"end,attr"`
			} `xml:"port"`
		} `xml:"nat"`
	} `xml:"forward"`
	Bridges []struct {
		Name  string `xml:"name,attr"`
		STP   string `xml:"stp,attr"`
		Delay string `xml:"delay,attr"`
	} `xml:"bridge"`
	IPs []struct {
		Address string `xml:"address,attr"`
		Netmask string `xml:"netmask,attr"`
		DHCPs   []struct {
			Ranges []struct {
				Start string `xml:"start,attr"`
				End   string `xml:"end,attr"`
			} `xml:"range"`
		} `xml:"dhcp"`
	} `xml:"ip"`
}

// netXMLElementCount NAT 网络 XML 的元素总数：
// network / name / forward / nat / port / bridge / ip / dhcp / range 共 9 个。
// assertXMLEscaped 用它换算出合法的尖括号字面量个数（9 × 2 = 18）。
const netXMLElementCount = 9

// unmarshalNetworkXML 反解网络 XML，失败即 Fatal（生成的 XML 必须始终合法）。
func unmarshalNetworkXML(t *testing.T, doc string) parsedNetworkXML {
	t.Helper()
	var n parsedNetworkXML
	if err := xml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatalf("生成的网络 XML 应当合法，解析失败: %v\nXML: %s", err, doc)
	}
	return n
}

// TestNetworkXMLFromParamsStructure 锁定 NAT 网络模板的每个关键节点。
//
// 风险点有三处，都属于"生成的 XML 语法完全合法、libvirt 也接受，但网络行为是错的"：
//  1. <forward mode='nat'> 一旦丢失或变成别的模式，虚拟机就没有出网 NAT
//     （mode 缺省是 isolated，只能内部互通），表现为新建的机器能 ping 网关不能上网；
//  2. <ip address> 与 <dhcp><range> 必须同网段。range 是用 replaceLastOctet
//     从网关字符串推出来的（.2 到 .254），推错了 dnsmasq 会拒绝启动整个网络；
//  3. 入参 cidr 被函数体直接丢弃（network.go:327 `_ = cidr`），掩码恒为 /24。
//     调用方传 /16 不会报错也不生效，这个"静默忽略"必须显式钉住，
//     否则将来有人以为支持自定义掩码，配出来的网络和界面上显示的不一致。
//
// 另外 NetworkXMLFromParams 的签名是 (name, cidr, gateway) string —— 没有 error
// 返回值，Marshal 失败时返回空串（network.go:355）。用例因此额外断言输出非空。
func TestNetworkXMLFromParamsStructure(t *testing.T) {
	tests := []struct {
		name        string
		netName     string
		cidr        string
		gateway     string
		wantName    string
		wantAddress string
		wantStart   string
		wantEnd     string
	}{
		{
			name:    "常规 NAT 网络",
			netName: "vmops-net", cidr: "192.168.100.0/24", gateway: "192.168.100.1",
			wantName: "vmops-net", wantAddress: "192.168.100.1",
			wantStart: "192.168.100.2", wantEnd: "192.168.100.254",
		},
		{
			name:    "网关留空：兜底 192.168.100.1，DHCP 范围随之兜底",
			netName: "auto-net", cidr: "", gateway: "",
			wantName: "auto-net", wantAddress: "192.168.100.1",
			wantStart: "192.168.100.2", wantEnd: "192.168.100.254",
		},
		{
			name:    "另一网段（DHCP 范围必须跟着网关变，不能写死 192.168.100.x）",
			netName: "net-10", cidr: "10.20.30.0/24", gateway: "10.20.30.1",
			wantName: "net-10", wantAddress: "10.20.30.1",
			wantStart: "10.20.30.2", wantEnd: "10.20.30.254",
		},
		{
			name:    "网关末位不是 1（范围仍固定 .2-.254，与网关末位无关）",
			netName: "gw254", cidr: "", gateway: "172.16.5.254",
			wantName: "gw254", wantAddress: "172.16.5.254",
			wantStart: "172.16.5.2", wantEnd: "172.16.5.254",
		},
		{
			name:    "cidr 传 /16 也被忽略，掩码恒为 255.255.255.0",
			netName: "big-net", cidr: "10.0.0.0/16", gateway: "10.0.0.1",
			wantName: "big-net", wantAddress: "10.0.0.1",
			wantStart: "10.0.0.2", wantEnd: "10.0.0.254",
		},
		{
			name:    "cidr 传垃圾字符串同样被忽略（不参与生成，不会报错）",
			netName: "junk-cidr", cidr: "not-a-cidr</ip>", gateway: "10.0.0.1",
			wantName: "junk-cidr", wantAddress: "10.0.0.1",
			wantStart: "10.0.0.2", wantEnd: "10.0.0.254",
		},
		{
			name:    "网络名为空（生成侧不校验，输出空 name 元素）",
			netName: "", cidr: "", gateway: "192.168.7.1",
			wantName: "", wantAddress: "192.168.7.1",
			wantStart: "192.168.7.2", wantEnd: "192.168.7.254",
		},
		{
			name:    "中文网络名（合法 UTF-8，网桥名仍是 virbr+数字）",
			netName: "生产网络", cidr: "", gateway: "192.168.9.1",
			wantName: "生产网络", wantAddress: "192.168.9.1",
			wantStart: "192.168.9.2", wantEnd: "192.168.9.254",
		},
		{
			name:    "网关不是 IPv4（不含点号）：原样落进 address 与 range",
			netName: "bad-gw", cidr: "", gateway: "nodot",
			// TODO(疑似缺陷): NetworkXMLFromParams 不校验 gateway 是否为合法 IPv4。
			//  唯一的防线在 handler/network.go（validIPv4），virt 层自身没有兜底；
			//  且 replaceLastOctet 在找不到 "." 时原样返回入参（network.go:379），
			//  于是 <dhcp><range start="nodot" end="nodot"/> 这种 XML 会被送进 libvirt
			//  （libvirt 会报错，属于"错得响"而非"错得静默"，但错误信息对用户没有指导性）。
			//  纵深防御的做法是在生成侧就用 net.ParseIP 校验，不合法直接拒绝。
			wantName: "bad-gw", wantAddress: "nodot",
			wantStart: "nodot", wantEnd: "nodot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NetworkXMLFromParams(tt.netName, tt.cidr, tt.gateway)
			if got == "" {
				t.Fatal("生成的网络 XML 期望非空（空串是 Marshal 失败的兜底返回值）")
			}

			n := unmarshalNetworkXML(t, got)

			// 网络名
			if len(n.Names) != 1 {
				t.Fatalf("<name> 节点数期望 1，实际 %d\nXML: %s", len(n.Names), got)
			}
			if n.Names[0] != tt.wantName {
				t.Errorf("网络名期望 %q，实际 %q", tt.wantName, n.Names[0])
			}

			// forward：模式必须是 nat，NAT 端口范围固定 1024-65535
			if len(n.Forwards) != 1 {
				t.Fatalf("<forward> 节点数期望 1，实际 %d\nXML: %s", len(n.Forwards), got)
			}
			fw := n.Forwards[0]
			if fw.Mode != natForwardMode {
				t.Errorf("forward 的 mode 属性期望 %q，实际 %q（不是 nat 则虚拟机无法出网）",
					natForwardMode, fw.Mode)
			}
			if len(fw.NATs) != 1 || len(fw.NATs[0].Ports) != 1 {
				t.Fatalf("期望 forward 下 1 个 <nat> 且其下 1 个 <port>，实际 nat=%d\nXML: %s",
					len(fw.NATs), got)
			}
			if p := fw.NATs[0].Ports[0]; p.Start != "1024" || p.End != "65535" {
				t.Errorf("nat/port 范围期望 start=1024 end=65535，实际 start=%q end=%q", p.Start, p.End)
			}

			// bridge：名字由 hashNetwork 算出，形态恒为 virbr+数字；stp/delay 固定
			if len(n.Bridges) != 1 {
				t.Fatalf("<bridge> 节点数期望 1，实际 %d\nXML: %s", len(n.Bridges), got)
			}
			br := n.Bridges[0]
			if !bridgeNameFormat.MatchString(br.Name) {
				t.Errorf("网桥名期望匹配 ^virbr[0-9]+$，实际 %q", br.Name)
			}
			if br.STP != bridgeSTP || br.Delay != bridgeDelay {
				t.Errorf("网桥 stp/delay 期望 %q/%q，实际 %q/%q", bridgeSTP, bridgeDelay, br.STP, br.Delay)
			}

			// ip + dhcp range：地址与掩码，以及必须同网段的 DHCP 范围
			if len(n.IPs) != 1 {
				t.Fatalf("<ip> 节点数期望 1，实际 %d\nXML: %s", len(n.IPs), got)
			}
			ip := n.IPs[0]
			if ip.Address != tt.wantAddress {
				t.Errorf("ip 的 address 属性期望 %q，实际 %q", tt.wantAddress, ip.Address)
			}
			if ip.Netmask != natNetmask {
				t.Errorf("ip 的 netmask 属性期望 %q（模板固定 /24），实际 %q", natNetmask, ip.Netmask)
			}
			if len(ip.DHCPs) != 1 || len(ip.DHCPs[0].Ranges) != 1 {
				t.Fatalf("期望 ip 下 1 个 <dhcp> 且其下 1 个 <range>，实际 dhcp=%d\nXML: %s",
					len(ip.DHCPs), got)
			}
			r := ip.DHCPs[0].Ranges[0]
			if r.Start != tt.wantStart || r.End != tt.wantEnd {
				t.Errorf("dhcp/range 期望 start=%q end=%q，实际 start=%q end=%q（与网关不同网段会让 dnsmasq 起不来）",
					tt.wantStart, tt.wantEnd, r.Start, r.End)
			}

			// 元素路径计数：骨架必须一个不多一个不少
			counts := xmlElementCounts(t, got)
			for path, want := range map[string]int{
				"network":                   1,
				"network/name":              1,
				"network/forward":           1,
				"network/forward/nat":       1,
				"network/forward/nat/port":  1,
				"network/bridge":            1,
				"network/ip":                1,
				"network/ip/dhcp":           1,
				"network/ip/dhcp/range":     1,
				"network/ip/dhcp/host":      0,
				"network/domain":            0,
				"network/portgroup":         0,
				"network/forward/interface": 0,
			} {
				if counts[path] != want {
					t.Errorf("节点 %s 的个数期望 %d，实际 %d\nXML: %s", path, want, counts[path], got)
				}
			}
			total := 0
			for _, c := range counts {
				total += c
			}
			if total != netXMLElementCount {
				t.Errorf("元素总数期望 %d，实际 %d\n全量计数: %v", netXMLElementCount, total, counts)
			}
		})
	}
}

// TestNetworkXMLFromParamsExactWireFormat 钉住送给 libvirt 的确切字符串形态。
//
// 风险点：docs/ 里的网络示例 XML 与答辩演示都直接引用这份输出。若有人把
// encoding/xml 换回字符串拼接（那会重新打开闭合标签注入），字符串形态必变，
// 而纯结构化断言可能仍然全过——这条精确比对是最后一道形态锁。
func TestNetworkXMLFromParamsExactWireFormat(t *testing.T) {
	const want = `<network><name>vmops-net</name>` +
		`<forward mode="nat"><nat><port start="1024" end="65535"></port></nat></forward>` +
		`<bridge name="virbr147" stp="on" delay="0"></bridge>` +
		`<ip address="192.168.100.1" netmask="255.255.255.0">` +
		`<dhcp><range start="192.168.100.2" end="192.168.100.254"></range></dhcp></ip></network>`

	got := NetworkXMLFromParams("vmops-net", "192.168.100.0/24", "192.168.100.1")
	if got != want {
		t.Errorf("网络 XML 字符串不符\n期望: %s\n实际: %s", want, got)
	}
	// 尖括号字面量个数与元素数对得上，顺带验证 netXMLElementCount 这个常量没写错
	if c, want := strings.Count(got, "<"), netXMLElementCount*2; c != want {
		t.Errorf("'<' 字面量个数期望 %d，实际 %d", want, c)
	}
}

// TestNetworkXMLFromParamsBlocksInjection 注入专项：网络名与网关塞元字符和闭合标签载荷。
//
// 风险点：这两个字段来自 handler/network.go 的 CreateNetwork 请求体。
// P1 之前这段 XML 是字符串拼接的，网络名里写
// `n</name><forward mode='bridge' dev='eth0'/><name>x`
// 就能把 NAT 网络改成桥接到宿主机物理网卡——虚拟机直接落到宿主机所在的生产网段，
// 绕过所有网络隔离；网关处注入 `<ip address='...'/>` 还能追加第二个 IP 段。
// 现在走 encoding/xml，需要的证据同 storage 那两个注入用例：
// 实体转义 + 数节点（forward/ip/name 各仍只有 1 个，总元素数仍是 9）
// + 反解后字段值原样还原 + forward mode 仍是 nat 而不是注入的 bridge。
func TestNetworkXMLFromParamsBlocksInjection(t *testing.T) {
	tests := []struct {
		name    string
		netName string
		gateway string
	}{
		{
			name:    "网络名注入 forward mode=bridge（想桥接到宿主机物理网卡）",
			netName: `n</name><forward mode='bridge' dev='eth0'/><name>x`,
			gateway: "192.168.100.1",
		},
		{
			name:    "网关属性闭合双引号后追加第二个 ip 段",
			netName: "ok-net",
			gateway: `192.168.100.1"/><ip address="10.0.0.1" netmask="255.0.0.0`,
		},
		{
			name:    "网关属性用单引号闭合",
			netName: "ok-net",
			gateway: `192.168.100.1'/><ip address='10.0.0.1`,
		},
		{
			name:    "网络名注入 domain 与 dhcp host（想劫持 DNS 与固定 IP 分配）",
			netName: `n</name><domain name='evil.local'/><name>x`,
			gateway: "192.168.100.1",
		},
		{
			name:    "整段闭合 network 再开一个新的",
			netName: `n</name></network><network><name>覆盖`,
			gateway: "192.168.100.1",
		},
		{
			name:    "五个 XML 元字符全上",
			netName: `&<>"'`,
			gateway: `&<>"'`,
		},
		{
			name:    "制表符/换行/回车（转成数字实体后必须能原样还原）",
			netName: "a\tb\nc\rd",
			gateway: "10.0.0.1\n",
		},
		{
			name:    "中文混元字符",
			netName: `生产网络<主>&<备>"'`,
			gateway: `192.168.1.1<中文>`,
		},
		{
			name:    "注释与 CDATA 载荷",
			netName: `a<!--x--><![CDATA[<forward mode='bridge'/>]]>b`,
			gateway: "192.168.100.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NetworkXMLFromParams(tt.netName, "", tt.gateway)
			if got == "" {
				t.Fatal("注入载荷不应导致返回空串（空串意味着 Marshal 失败）")
			}

			// 1) 元字符已转义，且尖括号字面量个数仍等于 9 个元素 × 2
			assertXMLEscaped(t, got, netXMLElementCount, tt.netName, tt.gateway)

			// 2) 数节点：注入没有新增任何节点
			counts := xmlElementCounts(t, got)
			for path, want := range map[string]int{
				"network":                   1,
				"network/name":              1,
				"network/forward":           1,
				"network/forward/nat":       1,
				"network/forward/nat/port":  1,
				"network/bridge":            1,
				"network/ip":                1,
				"network/ip/dhcp":           1,
				"network/ip/dhcp/range":     1,
				"network/ip/dhcp/host":      0,
				"network/domain":            0,
				"network/portgroup":         0,
				"network/forward/interface": 0,
			} {
				if counts[path] != want {
					t.Errorf("节点 %s 的个数期望 %d，实际 %d（说明注入改变了 XML 结构）\nXML: %s\n全量计数: %v",
						path, want, counts[path], got, counts)
				}
			}
			total := 0
			for _, c := range counts {
				total += c
			}
			if total != netXMLElementCount {
				t.Errorf("元素总数期望 %d，实际 %d\n全量计数: %v", netXMLElementCount, total, counts)
			}

			// 3) 反解后字段值还原为原文，关键属性未被撬开
			n := unmarshalNetworkXML(t, got)
			if len(n.Names) != 1 || n.Names[0] != tt.netName {
				t.Errorf("网络名反解后期望还原为原文 %q，实际 %v", tt.netName, n.Names)
			}
			if len(n.Forwards) != 1 {
				t.Fatalf("<forward> 节点数期望 1，实际 %d\nXML: %s", len(n.Forwards), got)
			}
			if n.Forwards[0].Mode != natForwardMode {
				t.Errorf("forward 的 mode 期望仍为 %q，实际 %q（注入改掉了转发模式，等于绕过网络隔离）",
					natForwardMode, n.Forwards[0].Mode)
			}
			if len(n.IPs) != 1 {
				t.Fatalf("<ip> 节点数期望 1，实际 %d（多出的 ip 段来自注入）\nXML: %s", len(n.IPs), got)
			}
			if n.IPs[0].Address != tt.gateway {
				t.Errorf("ip 的 address 期望还原为原文 %q，实际 %q", tt.gateway, n.IPs[0].Address)
			}
			if n.IPs[0].Netmask != natNetmask {
				t.Errorf("ip 的 netmask 期望仍为 %q，实际 %q", natNetmask, n.IPs[0].Netmask)
			}
			// 网桥名由 hashNetwork 生成，与输入形态无关，注入影响不到它
			if len(n.Bridges) != 1 || !bridgeNameFormat.MatchString(n.Bridges[0].Name) {
				t.Errorf("网桥名期望仍匹配 ^virbr[0-9]+$，实际 %v", n.Bridges)
			}
			// DHCP 范围是从网关串推出来的派生值，同样必须能原样还原（不含裸标签）
			if len(n.IPs[0].DHCPs) != 1 || len(n.IPs[0].DHCPs[0].Ranges) != 1 {
				t.Fatalf("dhcp/range 结构异常\nXML: %s", got)
			}
			r := n.IPs[0].DHCPs[0].Ranges[0]
			if wantStart, wantEnd := dhcpStart(tt.gateway), dhcpEnd(tt.gateway); r.Start != wantStart || r.End != wantEnd {
				t.Errorf("dhcp/range 反解后期望 start=%q end=%q，实际 start=%q end=%q",
					wantStart, wantEnd, r.Start, r.End)
			}
		})
	}
}

// TestNetworkXMLFromParamsBridgeNameCollision 记录网桥命名策略的两个性质：
// 同名必得同网桥（幂等，重复定义同一网络不会换网桥），
// 以及字符和取模的做法必然存在碰撞——字母重排后的名字会算出同一个网桥名。
//
// 风险点：libvirt 不允许两个网络用同一个 <bridge name>，第二个 net-define
// 会直接失败。hashNetwork 只有 200 个桶（sum%200+10），而且用的是字符和，
// 因此不仅是概率碰撞，而是「异位词一定碰撞」这种可预期的碰撞。
// 用户建两个名字互为重排的网络（net-a1 / net-1a）就会撞上，
// 而报错信息来自 libvirt，界面上看不出根因。
//
// TODO(改进): 网桥名应改为「取网络名的哈希后取更长的十六进制」或直接按
//
//	现存网桥列表递增分配（virbr0..virbrN 找空位），彻底消除可预期碰撞。
//	发现于毕业设计测试阶段，非阻塞问题，故用例先固化现状。
func TestNetworkXMLFromParamsBridgeNameCollision(t *testing.T) {
	// bridgeOf 从生成的 XML 里取出网桥名
	bridgeOf := func(netName string) string {
		t.Helper()
		n := unmarshalNetworkXML(t, NetworkXMLFromParams(netName, "", "192.168.100.1"))
		if len(n.Bridges) != 1 {
			t.Fatalf("网络 %q 的 <bridge> 节点数期望 1，实际 %d", netName, len(n.Bridges))
		}
		return n.Bridges[0].Name
	}

	t.Run("同一网络名多次生成得到同一网桥名（幂等）", func(t *testing.T) {
		first := bridgeOf("vmops-net")
		for i := 0; i < 5; i++ {
			if got := bridgeOf("vmops-net"); got != first {
				t.Fatalf("第 %d 次生成的网桥名期望 %q，实际 %q（网桥名必须稳定，否则重定义会换网卡）",
					i+2, first, got)
			}
		}
	})

	t.Run("网桥编号落在 hashNetwork 约定的 10..209 区间内", func(t *testing.T) {
		for _, netName := range []string{"", "a", "vmops-net", "生产网络", strings.Repeat("z", 300)} {
			br := bridgeOf(netName)
			if !bridgeNameFormat.MatchString(br) {
				t.Errorf("网络名 %q 的网桥名期望匹配 ^virbr[0-9]+$，实际 %q", netName, br)
				continue
			}
			num := strings.TrimPrefix(br, "virbr")
			if h := hashNetwork(netName); num != strconv.Itoa(h) {
				t.Errorf("网络名 %q 的网桥编号期望 %d（hashNetwork 结果），实际 %s", netName, h, num)
			}
			if h := hashNetwork(netName); h < 10 || h > 209 {
				t.Errorf("网络名 %q 的网桥编号 %d 越出约定区间 [10,209]", netName, h)
			}
		}
	})

	t.Run("异位词必定碰撞（现状缺陷，libvirt 会拒绝定义第二个网络）", func(t *testing.T) {
		pairs := [][2]string{
			{"net-a1", "net-1a"},
			{"ab", "ba"},
			{"prod-web", "prod-bew"},
		}
		for _, p := range pairs {
			b1, b2 := bridgeOf(p[0]), bridgeOf(p[1])
			if b1 != b2 {
				t.Errorf("网络名 %q 与 %q 是异位词，按 hashNetwork（字符和取模）应算出同一网桥名，实际 %q vs %q",
					p[0], p[1], b1, b2)
			}
		}
	})
}
