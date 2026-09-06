package virt

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/digitalocean/go-libvirt"
)

// domainRunning 判断域当前是否运行（供 attach/detach 选择 LIVE/CONFIG flags）。
// libvirt 对停机域使用 LIVE flags 会报"域没有在运行"。
func domainRunning(l *libvirt.Libvirt, dom libvirt.Domain) bool {
	state, _, err := l.DomainGetState(dom, 0)
	if err != nil {
		return false
	}
	return libvirt.DomainState(state) == libvirt.DomainRunning
}

// deviceFlags 按运行状态返回设备修改 flags：运行中 LIVE|CONFIG（热插拔并落配置），停机仅 CONFIG。
// 对应 libvirt VIR_DOMAIN_DEVICE_MODIFY_LIVE / VIR_DOMAIN_DEVICE_MODIFY_CONFIG。
func deviceFlags(running bool) uint32 {
	if running {
		return uint32(libvirt.DomainDeviceModifyLive | libvirt.DomainDeviceModifyConfig)
	}
	return uint32(libvirt.DomainDeviceModifyConfig)
}

// buildDiskXML 生成单磁盘设备 XML 片段（供 DomainAttachDeviceFlags 使用）。
// driver 类型按 DiskSpec.Driver（默认 qcow2，cdrom 用 raw）；cdrom 自动加 readonly。
func buildDiskXML(d DiskSpec) (string, error) {
	return xmlMarshal(diskXMLFromSpec(d))
}

// detachDiskXML 生成最小磁盘 XML 用于移除（libvirt 按 target dev 匹配，对应 virsh detach-device）。
func detachDiskXML(target string) string {
	dx := diskXML{Type: "file", Device: "disk"}
	dx.Target.Dev = target
	out, err := xmlMarshal(dx)
	if err != nil {
		return ""
	}
	return out
}

// buildInterfaceXML 生成单网卡 XML 片段（供 DomainAttachDeviceFlags 使用）。
func buildInterfaceXML(i InterfaceSpec) (string, error) {
	return xmlMarshal(interfaceXMLFromSpec(i))
}

// detachInterfaceXML 生成移除网卡的最小 XML（对应 virsh detach-device）。
// libvirt 移除时要求接口带 <source> 元素，否则报"接口类型='network' 需要 'source' 元素"，
// 因此需按 MAC 先从域 XML 取回完整网卡配置再拼装。
func (v *Virt) detachInterfaceXML(domain, mac string) (string, error) {
	xmlstr, err := v.GetDomainXML(domain)
	if err != nil {
		return "", err
	}
	spec, err := ParseDomainXML(xmlstr)
	if err != nil {
		return "", err
	}
	for _, i := range spec.Interfaces {
		if strings.EqualFold(i.MAC, mac) {
			return xmlMarshal(interfaceXMLFromSpec(i))
		}
	}
	// 域里没有该 MAC：按调用方意图生成（类型与 source 交给调用方保证）
	ifx := interfaceXML{Type: "network"}
	ifx.MAC = &interfaceMacXML{Address: mac}
	return xmlMarshal(ifx)
}

// AttachDisk 向虚拟机挂载磁盘设备（对应 virsh attach-device）。
// 运行中热插拔（LIVE|CONFIG），停机时仅落配置（CONFIG）。
func (v *Virt) AttachDisk(domain string, d DiskSpec) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	devXML, err := buildDiskXML(d)
	if err != nil {
		return fmt.Errorf("生成磁盘 XML 失败: %w", err)
	}
	if err := l.DomainAttachDeviceFlags(dom, devXML, deviceFlags(domainRunning(l, dom))); err != nil {
		return fmt.Errorf("挂载磁盘失败（对应 virsh attach-device）: %w", err)
	}
	return nil
}

// DetachDisk 从虚拟机移除磁盘设备（对应 virsh detach-device，按 target dev 匹配）。
func (v *Virt) DetachDisk(domain, target string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	if err := l.DomainDetachDeviceFlags(dom, detachDiskXML(target), deviceFlags(domainRunning(l, dom))); err != nil {
		return fmt.Errorf("移除磁盘失败（对应 virsh detach-device）: %w", err)
	}
	return nil
}

// AttachInterface 向虚拟机添加网卡（对应 virsh attach-interface，运行中热插拔、停机落配置）。
func (v *Virt) AttachInterface(domain string, i InterfaceSpec) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	devXML, err := buildInterfaceXML(i)
	if err != nil {
		return fmt.Errorf("生成网卡 XML 失败: %w", err)
	}
	if err := l.DomainAttachDeviceFlags(dom, devXML, deviceFlags(domainRunning(l, dom))); err != nil {
		return fmt.Errorf("添加网卡失败（对应 virsh attach-interface）: %w", err)
	}
	return nil
}

// DetachInterface 从虚拟机移除网卡（对应 virsh detach-interface，按 MAC 地址匹配）。
func (v *Virt) DetachInterface(domain, mac string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	devXML, err := v.detachInterfaceXML(domain, mac)
	if err != nil {
		return err
	}
	if err := l.DomainDetachDeviceFlags(dom, devXML, deviceFlags(domainRunning(l, dom))); err != nil {
		return fmt.Errorf("移除网卡失败（对应 virsh detach-interface）: %w", err)
	}
	return nil
}

// domainDevicePresence 解析域 XML 判断 guest-agent 通道与 rng 是否已挂载（避免重复 attach）。
// 注意通道/rng 挂在 <devices> 之下，解析结构必须保留这一层级。
type domainDevicePresence struct {
	Devices struct {
		Channels []struct {
			Target struct {
				Name string `xml:"name,attr"`
			} `xml:"target"`
		} `xml:"channel"`
		Rng *struct{} `xml:"rng"`
	} `xml:"devices"`
}

// EnsureStandardDevices 补齐标准设备：guest-agent 通道（org.qemu.guest_agent.0）与
// virtio-rng（/dev/urandom）。新建虚拟机已由 BuildDomainXML 显式生成，此方法用于补齐
// 存量虚拟机，使其与新装机配置一致；幂等，已存在的设备自动跳过。
// 返回本次实际添加的设备名列表（空列表 = 已是标准配置）。
// 串口/控制台与 memballoon 由 libvirt 隐式默认提供，无需处理。
func (v *Virt) EnsureStandardDevices(domain string) ([]string, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return nil, fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}

	xmlstr, err := v.GetDomainXML(domain)
	if err != nil {
		return nil, err
	}
	var presence domainDevicePresence
	if err := xml.Unmarshal([]byte(xmlstr), &presence); err != nil {
		return nil, fmt.Errorf("解析域 XML 失败: %w", err)
	}
	hasAgent, hasRng := false, false
	for _, ch := range presence.Devices.Channels {
		if ch.Target.Name == "org.qemu.guest_agent.0" {
			hasAgent = true
		}
	}
	hasRng = presence.Devices.Rng != nil

	flags := deviceFlags(domainRunning(l, dom))
	attached := make([]string, 0, 2)
	if !hasAgent {
		agentXML, err := xmlMarshal(channelXML{Type: "unix", Target: channelTargetXML{Type: "virtio", Name: "org.qemu.guest_agent.0"}})
		if err != nil {
			return nil, fmt.Errorf("生成 guest-agent 通道 XML 失败: %w", err)
		}
		if err := l.DomainAttachDeviceFlags(dom, agentXML, flags); err != nil {
			return attached, fmt.Errorf("添加 guest-agent 通道失败（对应 virsh attach-device）: %w", err)
		}
		attached = append(attached, "guest-agent 通道")
	}
	if !hasRng {
		rngDevXML, err := xmlMarshal(rngXML{Model: "virtio", Backend: rngBackendXML{Model: "random", Value: "/dev/urandom"}})
		if err != nil {
			return nil, fmt.Errorf("生成 virtio-rng XML 失败: %w", err)
		}
		if err := l.DomainAttachDeviceFlags(dom, rngDevXML, flags); err != nil {
			return attached, fmt.Errorf("添加 virtio-rng 失败（对应 virsh attach-device）: %w", err)
		}
		attached = append(attached, "virtio-rng")
	}
	return attached, nil
}

// SetVcpus 调整虚拟机 CPU 核数（对应 virsh setvcpus）。
// 运行中用 LIVE|CONFIG（可热调至启动时最大核数以内）；停机仅 CONFIG 落配置。
func (v *Virt) SetVcpus(domain string, n int) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	flags := uint32(libvirt.DomainAffectConfig)
	if domainRunning(l, dom) {
		flags |= uint32(libvirt.DomainAffectLive)
	}
	if err := l.DomainSetVcpusFlags(dom, uint32(n), flags); err != nil {
		return fmt.Errorf("设置 CPU 核数失败（对应 virsh setvcpus，运行中热增受上限限制、热减需关机）: %w", err)
	}
	return nil
}

// SetMemory 调整虚拟机内存（对应 virsh setmem）。
// 运行中用 LIVE|CONFIG（可热调至启动时最大内存以内）；停机仅 CONFIG 落配置。
// 旧驱动不支持 flags 时回退到基础 DomainSetMemory。
func (v *Virt) SetMemory(domain string, mb int) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	memKiB := uint64(mb) * 1024
	flags := uint32(libvirt.DomainMemConfig)
	if domainRunning(l, dom) {
		flags |= uint32(libvirt.DomainMemLive)
	}
	if err := l.DomainSetMemoryFlags(dom, memKiB, flags); err != nil {
		// 运行中增大内存不能超过开机时大小（超出需 QEMU 内存热插，本平台未启用 NUMA），回退基础调用
		if err2 := l.DomainSetMemory(dom, memKiB); err2 != nil {
			return fmt.Errorf("设置内存失败（对应 virsh setmem，运行中增大不能超过开机时大小，超出请关机后调整）: %w", err2)
		}
	}
	return nil
}
