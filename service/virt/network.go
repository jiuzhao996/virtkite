// 本文件负责 libvirt 网络的查询与生命周期操作：枚举/读取网络详情
// （list/get，对应 virsh net-list / net-dumpxml）与 start/stop/autostart/define/undefine
// （对应 virsh net-start / net-destroy / net-autostart / net-define / net-undefine）。
// 网络 XML 生成见 network_xml.go，DHCP 租约与 guest-agent 查询见 network_lease.go，
// 域网络拓扑见 network_topo.go。
package virt

import (
	"encoding/xml"
	"fmt"

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
