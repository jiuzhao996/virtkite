// 本文件负责域（虚拟机）的网络挂接拓扑：枚举所有域（含关机）的网络接口
// （ListAllDomainNetworks，对应 virsh list --all + domiflist），供网络拓扑视图使用。
// 网络查询与生命周期见 network.go，DHCP 租约与 guest-agent 查询见 network_lease.go。
package virt

import (
	"fmt"

	"github.com/digitalocean/go-libvirt"
)

// DomainNetInfo 域的网络挂接（拓扑视图用：VM 挂哪些网络 + MAC + 运行态）。
type DomainNetInfo struct {
	Name       string          `json:"name"`
	State      string          `json:"state"`
	Interfaces []InterfaceSpec `json:"interfaces"`
}

// ListAllDomainNetworks 枚举所有域（含关机）的网络接口（对应 virsh list --all + domiflist）。
// 关机的域也能读到持久配置里的网卡，故拓扑图不受运行态限制。
func (v *Virt) ListAllDomainNetworks() ([]DomainNetInfo, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}
	flags := libvirt.ConnectListDomainsActive | libvirt.ConnectListDomainsInactive
	domains, _, err := l.ConnectListAllDomains(1, flags)
	if err != nil {
		return nil, fmt.Errorf("枚举虚拟机失败: %w", err)
	}
	out := make([]DomainNetInfo, 0, len(domains))
	for _, d := range domains {
		info := DomainNetInfo{Name: d.Name}
		if state, _, err := l.DomainGetState(d, 0); err == nil {
			info.State = StateToPlatform(state)
		}
		// 单个域 XML 读取/解析失败不影响其余域（拓扑图降级为无网卡）
		if xmlstr, err := l.DomainGetXMLDesc(d, 0); err == nil {
			if spec, perr := ParseDomainXML(xmlstr); perr == nil && spec != nil {
				info.Interfaces = spec.Interfaces
			}
		}
		out = append(out, info)
	}
	return out, nil
}
