package virt

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/digitalocean/go-libvirt"
)

// NetworkInfo 网络详情。
type NetworkInfo struct {
	Name       string `json:"name"`
	Active     bool   `json:"active"`
	Persistent bool   `json:"persistent"`
	Autostart  bool   `json:"autostart"`
	Bridge     string `json:"bridge"`
	Forward    string `json:"forward"`
	Gateway    string `json:"gateway"`
	CIDR       string `json:"cidr"`
	DhcpStart  string `json:"dhcp_start"`
	DhcpEnd    string `json:"dhcp_end"`
	XML        string `json:"xml,omitempty"`
}

// ListNetworks 返回所有网络详情（对应 virsh net-list --all）。
func (v *Virt) ListNetworks() ([]NetworkInfo, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}

	flags := libvirt.ConnectListNetworksActive | libvirt.ConnectListNetworksInactive
	networks, _, err := l.ConnectListAllNetworks(1, flags)
	if err != nil {
		return nil, fmt.Errorf("枚举网络失败（对应 virsh net-list --all）: %w", err)
	}

	infos := make([]NetworkInfo, 0, len(networks))
	for _, n := range networks {
		info, err := v.getNetworkInfo(l, n)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// GetNetwork 返回指定网络详情（含 XML）。
func (v *Virt) GetNetwork(name string) (*NetworkInfo, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}
	n, err := l.NetworkLookupByName(name)
	if err != nil {
		return nil, fmt.Errorf("网络 %s 不存在: %w", name, err)
	}
	info, err := v.getNetworkInfo(l, n)
	if err != nil {
		return nil, err
	}
	// 补充完整 XML
	if xmlstr, err := l.NetworkGetXMLDesc(n, 0); err == nil {
		info.XML = xmlstr
	}
	return &info, nil
}

// getNetworkInfo 聚合单网络详情，解析 XML 提取 forward/gateway/cidr。
func (v *Virt) getNetworkInfo(l *libvirt.Libvirt, net libvirt.Network) (NetworkInfo, error) {
	info := NetworkInfo{Name: net.Name}

	active, err := l.NetworkIsActive(net)
	if err == nil {
		info.Active = active == 1
	}
	persistent, err := l.NetworkIsPersistent(net)
	if err == nil {
		info.Persistent = persistent == 1
	}
	// 自动启动（对应 virsh net-autostart），解析失败忽略
	autostart, err := l.NetworkGetAutostart(net)
	if err == nil {
		info.Autostart = autostart == 1
	}
	bridge, err := l.NetworkGetBridgeName(net)
	if err == nil {
		info.Bridge = bridge
	}

	xmlstr, err := l.NetworkGetXMLDesc(net, 0)
	if err == nil {
		var n struct {
			Forward struct {
				Mode string `xml:"mode,attr"`
			} `xml:"forward"`
			IPs []struct {
				Address string `xml:"address,attr"`
				Netmask string `xml:"netmask,attr"`
				DHCP    struct {
					Ranges []struct {
						Start string `xml:"start,attr"`
						End   string `xml:"end,attr"`
					} `xml:"range"`
				} `xml:"dhcp"`
			} `xml:"ip"`
		}
		if err := xml.Unmarshal([]byte(xmlstr), &n); err == nil {
			info.Forward = n.Forward.Mode
			if len(n.IPs) > 0 {
				info.Gateway = n.IPs[0].Address
				info.CIDR = n.IPs[0].Netmask
				// 解析 DHCP 范围（对应 virsh net-dumpxml 的 <dhcp><range>）
				if len(n.IPs[0].DHCP.Ranges) > 0 {
					info.DhcpStart = n.IPs[0].DHCP.Ranges[0].Start
					info.DhcpEnd = n.IPs[0].DHCP.Ranges[0].End
				}
			}
		}
	}
	return info, nil
}

// StartNetwork 启动网络（对应 virsh net-start）。
func (v *Virt) StartNetwork(name string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	n, err := l.NetworkLookupByName(name)
	if err != nil {
		return fmt.Errorf("网络 %s 不存在: %w", name, err)
	}
	if err := l.NetworkCreate(n); err != nil {
		return fmt.Errorf("启动网络失败: %w", err)
	}
	return nil
}

// StopNetwork 停止网络（对应 virsh net-destroy）。
func (v *Virt) StopNetwork(name string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	n, err := l.NetworkLookupByName(name)
	if err != nil {
		return fmt.Errorf("网络 %s 不存在: %w", name, err)
	}
	if err := l.NetworkDestroy(n); err != nil {
		return fmt.Errorf("停止网络失败: %w", err)
	}
	return nil
}

// SetNetworkAutostart 设置网络自启动（对应 virsh net-autostart <net> on|off）。
func (v *Virt) SetNetworkAutostart(name string, autostart bool) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	n, err := l.NetworkLookupByName(name)
	if err != nil {
		return fmt.Errorf("网络 %s 不存在: %w", name, err)
	}
	flag := int32(0)
	if autostart {
		flag = 1
	}
	if err := l.NetworkSetAutostart(n, flag); err != nil {
		return fmt.Errorf("设置网络自启动失败: %w", err)
	}
	return nil
}

// DeleteNetwork 删除网络定义（对应 virsh net-undefine）。运行中的网络先停止再删除。
func (v *Virt) DeleteNetwork(name string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	n, err := l.NetworkLookupByName(name)
	if err != nil {
		return fmt.Errorf("网络 %s 不存在: %w", name, err)
	}

	active, err := l.NetworkIsActive(n)
	if err == nil && active == 1 {
		if err := l.NetworkDestroy(n); err != nil {
			return fmt.Errorf("停止网络失败: %w", err)
		}
	}

	if err := l.NetworkUndefine(n); err != nil {
		return fmt.Errorf("删除网络失败: %w", err)
	}
	return nil
}

// DefineNetwork 定义网络（对应 virsh net-define + net-start + autostart）。
func (v *Virt) DefineNetwork(xml string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	n, err := l.NetworkDefineXML(xml)
	if err != nil {
		return fmt.Errorf("定义网络失败: %w", err)
	}
	if err := l.NetworkCreate(n); err != nil {
		return fmt.Errorf("启动网络失败: %w", err)
	}
	if err := l.NetworkSetAutostart(n, 1); err != nil {
		return fmt.Errorf("设置自动启动失败: %w", err)
	}
	return nil
}

// DefineNetworkXML 定义网络（含 XML 校验，供编辑用，不自动启动）。
func (v *Virt) DefineNetworkXML(xml string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	if _, err := l.NetworkDefineXML(xml); err != nil {
		return fmt.Errorf("定义网络失败: %w", err)
	}
	return nil
}

// UpdateNetwork 编辑网络（对应 virsh net-destroy + net-undefine + net-define + net-start）。
// 运行中的网络先停止再重建，并保留原 autostart 设置。
func (v *Virt) UpdateNetwork(name, xml string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	n, err := l.NetworkLookupByName(name)
	if err != nil {
		return fmt.Errorf("网络 %s 不存在: %w", name, err)
	}

	// 记录原 autostart，重建后恢复
	autostart := int32(0)
	if a, err := l.NetworkGetAutostart(n); err == nil {
		autostart = a
	}

	// 运行中先停止（对应 virsh net-destroy）
	active, err := l.NetworkIsActive(n)
	if err == nil && active == 1 {
		if err := l.NetworkDestroy(n); err != nil {
			return fmt.Errorf("停止网络失败: %w", err)
		}
	}
	// 删除旧定义（对应 virsh net-undefine）
	if err := l.NetworkUndefine(n); err != nil {
		return fmt.Errorf("删除网络定义失败: %w", err)
	}
	// 按新 XML 重新定义并启动（对应 virsh net-define + net-start）
	nn, err := l.NetworkDefineXML(xml)
	if err != nil {
		return fmt.Errorf("定义网络失败: %w", err)
	}
	if err := l.NetworkCreate(nn); err != nil {
		return fmt.Errorf("启动网络失败: %w", err)
	}
	// 恢复原 autostart 设置
	if err := l.NetworkSetAutostart(nn, autostart); err != nil {
		return fmt.Errorf("恢复自动启动设置失败: %w", err)
	}
	return nil
}

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

// netForwardXML <forward mode='nat'><nat>...</nat></forward>
type netForwardXML struct {
	Mode string    `xml:"mode,attr"`
	NAT  netNatXML `xml:"nat"`
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
	XMLName xml.Name      `xml:"network"`
	Name    string        `xml:"name"`
	Forward netForwardXML `xml:"forward"`
	Bridge  netBridgeXML  `xml:"bridge"`
	IP      netIPXML      `xml:"ip"`
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
		Forward: netForwardXML{
			Mode: natForwardMode,
			NAT:  netNatXML{Port: netPortXML{Start: natPortStart, End: natPortEnd}},
		},
		Bridge: netBridgeXML{
			Name:  fmt.Sprintf("virbr%d", hashNetwork(name)),
			STP:   bridgeSTP,
			Delay: bridgeDelay,
		},
		IP: netIPXML{
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

// DhcpLease DHCP 租约条目（对应 virsh net-dhcp-leases 输出的一行）。
type DhcpLease struct {
	MAC      string
	IP       string
	Hostname string
}

// ListDHCPLeases 枚举所有活跃网络的 DHCP 租约并归并（对应 virsh net-dhcp-leases <net>）。
// 用于把 VM 实际获取到的 IP 回填进数据库（vms.ip）——Web 终端 SSH 目标白名单
// 依赖该字段做「VM 已记录 IP 则精确匹配」，无回填则该分支永远不可达。
func (v *Virt) ListDHCPLeases() ([]DhcpLease, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}

	networks, _, err := l.ConnectListAllNetworks(1, libvirt.ConnectListNetworksActive)
	if err != nil {
		return nil, fmt.Errorf("枚举活跃网络失败: %w", err)
	}

	now := time.Now().Unix()
	leases := make([]DhcpLease, 0, 8)
	for _, n := range networks {
		items, _, err := l.NetworkGetDhcpLeases(n, libvirt.OptString{}, -1, 0)
		if err != nil {
			// 单个网络失败（如无 DHCP 配置）不影响其余网络的租约收集
			continue
		}
		for _, it := range items {
			if it.Ipaddr == "" {
				continue
			}
			// 过滤已过期租约（Expirytime 为 0 表示无过期时间，不过滤）
			if it.Expirytime > 0 && it.Expirytime < now {
				continue
			}
			lease := DhcpLease{IP: it.Ipaddr}
			if len(it.Mac) > 0 {
				lease.MAC = it.Mac[0]
			}
			if len(it.Hostname) > 0 {
				lease.Hostname = it.Hostname[0]
			}
			leases = append(leases, lease)
		}
	}
	return leases, nil
}
