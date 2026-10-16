// 本文件负责从 libvirt 侧获取虚拟机 IP 的两条途径：枚举各活跃网络的 DHCP 租约
// （ListDHCPLeases，对应 virsh net-dhcp-leases，用于把 VM 实际 IP 回填进 vms.ip）与通过
// qemu-guest-agent 查询网卡 IPv4（ListGuestIPs，对应 virsh domifaddr --source agent，
// 静态 IP 的 VM 只能靠它）。网络查询与生命周期见 network.go。
package virt

import (
	"fmt"
	"time"

	"github.com/digitalocean/go-libvirt"
)

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

// ListGuestIPs 通过 qemu-guest-agent 查询虚拟机网卡 IPv4（对应 virsh domifaddr --source agent）。
// 前提：域 XML 已含 guest-agent 通道（建机默认启用）且客户机内安装并运行了 qemu-guest-agent。
// 静态 IP 的虚拟机不会出现在 DHCP 租约里，QGA 是获取其地址的唯一自动途径。
// agent 未安装/未运行时 libvirt 返回错误，调用方静默降级即可。
func (v *Virt) ListGuestIPs(name string) ([]string, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return nil, fmt.Errorf("虚拟机 %s 不存在: %w", name, err)
	}
	ifaces, err := l.DomainInterfaceAddresses(dom, uint32(libvirt.DomainInterfaceAddressesSrcAgent), 0)
	if err != nil {
		return nil, fmt.Errorf("查询 guest-agent 接口失败（客户机内需安装并运行 qemu-guest-agent）: %w", err)
	}
	var ips []string
	for _, it := range ifaces {
		for _, a := range it.Addrs {
			// Type 0 = IPv4；跳过环回
			if a.Type == 0 && a.Addr != "" && a.Addr != "127.0.0.1" {
				ips = append(ips, a.Addr)
			}
		}
	}
	return ips, nil
}
