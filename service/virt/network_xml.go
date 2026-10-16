// 本文件负责 libvirt 网络定义的 XML 生成：NAT / 隔离 / 桥接三种转发模式的
// <network> XML 拼装（encoding/xml 序列化，name/gateway 等外部可控输入由标准库自动
// 转义，无 XML 注入面），以及网桥后缀、DHCP 范围等模板助手。网络查询与生命周期操作
// （list/get/define/start/stop/…）见 network.go，DHCP 租约与 guest-agent 查询见 network_lease.go，
// 域网络拓扑见 network_topo.go。
package virt

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// 以下为 NAT 网络 XML 生成结构（按 AGENTS.md「XML 用标准库 encoding/xml」，
// 参照 spec.go 的 BuildDomainXML 写法）。用 xml.Marshal 而非字符串拼接：
// 网络名/网关中的 XML 元字符由标准库自动转义，无法闭合标签注入额外节点。

// netPortXML <port start='1024' end='65535'/>
type netPortXML struct {
	Start int `xml:"start,attr"`
	End   int `xml:"end,attr"`
}

// netNatXML <nat><port .../></nat>
type netNatXML struct {
	Port netPortXML `xml:"port"`
}

// netForwardXML <forward mode='nat'><nat>...</nat></forward>；isolated 无 <forward>，bridge 无 <nat>
type netForwardXML struct {
	Mode string     `xml:"mode,attr"`
	NAT  *netNatXML `xml:"nat,omitempty"`
}

// netBridgeXML <bridge name='virbr10' stp='on' delay='0'/>
type netBridgeXML struct {
	Name  string `xml:"name,attr"`
	STP   string `xml:"stp,attr"`
	Delay string `xml:"delay,attr"`
}

// netRangeXML <range start='192.168.100.2' end='192.168.100.254'/>
type netRangeXML struct {
	Start string `xml:"start,attr"`
	End   string `xml:"end,attr"`
}

// netDhcpXML <dhcp><range .../></dhcp>
type netDhcpXML struct {
	Range netRangeXML `xml:"range"`
}

// netIPXML <ip address='192.168.100.1' netmask='255.255.255.0'><dhcp>...</dhcp></ip>
type netIPXML struct {
	Address string     `xml:"address,attr"`
	Netmask string     `xml:"netmask,attr"`
	DHCP    netDhcpXML `xml:"dhcp"`
}

// networkXML 网络定义根元素（对应 virsh net-define 的输入 XML）。
type networkXML struct {
	XMLName xml.Name       `xml:"network"`
	Name    string         `xml:"name"`
	Forward *netForwardXML `xml:"forward,omitempty"`
	Bridge  netBridgeXML   `xml:"bridge"`
	IP      *netIPXML      `xml:"ip,omitempty"`
}

// NAT 网络模板固定取值（命名常量，避免散落字面量）。
const (
	natForwardMode = "nat"           // <forward mode>
	natPortStart   = 1024            // <nat><port start>
	natPortEnd     = 65535           // <nat><port end>
	natNetmask     = "255.255.255.0" // <ip netmask>：模板固定 /24
	natDefaultGW   = "192.168.100.1" // gateway 入参为空时的默认网关
	bridgeSTP      = "on"            // <bridge stp>
	bridgeDelay    = "0"             // <bridge delay>
)

// NetworkXMLFromParams 根据参数生成 NAT 网络 XML（类似 virsh net-create 的 NAT 模板）。
// 走 encoding/xml 序列化，name/gateway 为外部可控输入，由标准库自动转义，无 XML 注入面。
func NetworkXMLFromParams(name, cidr, gateway string) string {
	// cidr 形如 192.168.100.0/24；当前模板固定 /24 掩码，暂不使用该入参
	_ = cidr
	ipPart := gateway
	if ipPart == "" {
		ipPart = natDefaultGW
	}

	nx := networkXML{
		Name: name,
		Forward: &netForwardXML{
			Mode: natForwardMode,
			NAT:  &netNatXML{Port: netPortXML{Start: natPortStart, End: natPortEnd}},
		},
		Bridge: netBridgeXML{
			Name:  fmt.Sprintf("virbr%d", hashNetwork(name)),
			STP:   bridgeSTP,
			Delay: bridgeDelay,
		},
		IP: &netIPXML{
			Address: ipPart,
			Netmask: natNetmask,
			DHCP:    netDhcpXML{Range: netRangeXML{Start: dhcpStart(ipPart), End: dhcpEnd(ipPart)}},
		},
	}

	out, err := xml.Marshal(nx)
	if err != nil {
		// networkXML 只含字符串/整数字段，Marshal 不会失败；
		// 兜底返回空串，由调用方 DefineNetwork 报「定义网络失败」，不生成半截 XML。
		return ""
	}
	return string(out)
}

// 网络转发模式（与前端「新建网络」选项一致）。
const (
	NetModeNAT      = "nat"      // NAT：借宿主出网（默认）
	NetModeIsolated = "isolated" // 隔离：仅内部互通，不出网（无 <forward>）
	NetModeBridge   = "bridge"   // 桥接：直接接已存在的宿主机网桥（IP/DHCP 由宿主网桥提供）
)

// NetworkXMLForMode 按转发模式生成网络 XML（encoding/xml 序列化，无注入面）。
//   - nat      → <forward mode='nat'> + <nat>端口范围 + 内置 DHCP
//   - isolated → 无 <forward>（仅本网络内互通）+ 内置 DHCP
//   - bridge   → <forward mode='bridge'/> + <bridge name='<宿主网桥>'/>，无 IP/DHCP（宿主网桥负责）
//
// hostBridge 仅 bridge 模式需要（须是宿主机上已存在的网桥，如 br0）。
func NetworkXMLForMode(name, mode, gateway, hostBridge string) (string, error) {
	nx := networkXML{Name: name}
	switch mode {
	case "", NetModeNAT:
		ip := gateway
		if ip == "" {
			ip = natDefaultGW
		}
		nx.Forward = &netForwardXML{
			Mode: natForwardMode,
			NAT:  &netNatXML{Port: netPortXML{Start: natPortStart, End: natPortEnd}},
		}
		nx.Bridge = netBridgeXML{Name: fmt.Sprintf("virbr%d", hashNetwork(name)), STP: bridgeSTP, Delay: bridgeDelay}
		nx.IP = &netIPXML{Address: ip, Netmask: natNetmask, DHCP: netDhcpXML{Range: netRangeXML{Start: dhcpStart(ip), End: dhcpEnd(ip)}}}
	case NetModeIsolated:
		ip := gateway
		if ip == "" {
			ip = natDefaultGW
		}
		// 无 <forward>：libvirt 视为隔离网络，仅本网络内互通
		nx.Bridge = netBridgeXML{Name: fmt.Sprintf("virbr%d", hashNetwork(name)), STP: bridgeSTP, Delay: bridgeDelay}
		nx.IP = &netIPXML{Address: ip, Netmask: natNetmask, DHCP: netDhcpXML{Range: netRangeXML{Start: dhcpStart(ip), End: dhcpEnd(ip)}}}
	case NetModeBridge:
		if strings.TrimSpace(hostBridge) == "" {
			return "", fmt.Errorf("桥接网络需要指定宿主机网桥名（如 br0）")
		}
		nx.Forward = &netForwardXML{Mode: NetModeBridge}
		nx.Bridge = netBridgeXML{Name: hostBridge}
	default:
		return "", fmt.Errorf("不支持的转发模式: %s", mode)
	}
	out, err := xml.Marshal(nx)
	if err != nil {
		return "", fmt.Errorf("生成网络 XML 失败: %w", err)
	}
	return string(out), nil
}

// hashNetwork 根据名称生成稳定网桥后缀
func hashNetwork(name string) int {
	sum := 0
	for _, r := range name {
		sum += int(r)
	}
	return sum%200 + 10
}

// dhcpStart / dhcpEnd 基于网关生成 DHCP 范围
func dhcpStart(gw string) string {
	return replaceLastOctet(gw, 2)
}
func dhcpEnd(gw string) string {
	return replaceLastOctet(gw, 254)
}

func replaceLastOctet(ip string, last int) string {
	idx := strings.LastIndex(ip, ".")
	if idx < 0 {
		return ip
	}
	return fmt.Sprintf("%s.%d", ip[:idx], last)
}
