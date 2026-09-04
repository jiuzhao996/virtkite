package virt

import (
	"fmt"

	"github.com/digitalocean/go-libvirt"
)

// attachDetachFlags 热插拔/修改设备的 flags：同时影响运行实例与持久配置。
// 对应 libvirt VIR_DOMAIN_DEVICE_MODIFY_LIVE | VIR_DOMAIN_DEVICE_MODIFY_CONFIG。
func attachDetachFlags() uint32 {
	return uint32(libvirt.DomainDeviceModifyLive | libvirt.DomainDeviceModifyConfig)
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

// detachInterfaceXML 生成最小网卡 XML 用于移除（libvirt 按 MAC 地址匹配，对应 virsh detach-device）。
func detachInterfaceXML(mac string) string {
	ifx := interfaceXML{Type: "network"}
	ifx.MAC = &interfaceMacXML{Address: mac}
	out, err := xmlMarshal(ifx)
	if err != nil {
		return ""
	}
	return out
}

// AttachDisk 向虚拟机挂载磁盘设备（对应 virsh attach-device）。
// 使用 LIVE|CONFIG flags：运行中热插拔，关机时配置同样落盘；VM 未运行时仅落配置。
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
	if err := l.DomainAttachDeviceFlags(dom, devXML, attachDetachFlags()); err != nil {
		return fmt.Errorf("挂载磁盘失败（对应 virsh attach-device，运行中热插拔、关机时落配置）: %w", err)
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
	if err := l.DomainDetachDeviceFlags(dom, detachDiskXML(target), attachDetachFlags()); err != nil {
		return fmt.Errorf("移除磁盘失败（对应 virsh detach-device）: %w", err)
	}
	return nil
}

// AttachInterface 向虚拟机添加网卡（对应 virsh attach-interface，运行中热插拔、关机时落配置）。
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
	if err := l.DomainAttachDeviceFlags(dom, devXML, attachDetachFlags()); err != nil {
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
	if err := l.DomainDetachDeviceFlags(dom, detachInterfaceXML(mac), attachDetachFlags()); err != nil {
		return fmt.Errorf("移除网卡失败（对应 virsh detach-interface）: %w", err)
	}
	return nil
}

// SetVcpus 调整虚拟机 CPU 核数（对应 virsh setvcpus，live+config flags）。
func (v *Virt) SetVcpus(domain string, n int) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	flags := uint32(libvirt.DomainAffectLive | libvirt.DomainAffectConfig)
	if err := l.DomainSetVcpusFlags(dom, uint32(n), flags); err != nil {
		return fmt.Errorf("设置 CPU 核数失败（对应 virsh setvcpus）: %w", err)
	}
	return nil
}

// SetMemory 调整虚拟机内存（对应 virsh setmem，live+config flags）。
// 优先使用 flags 版本，旧驱动不支持时回退到基础 DomainSetMemory。
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
	flags := uint32(libvirt.DomainMemLive | libvirt.DomainMemConfig)
	if err := l.DomainSetMemoryFlags(dom, memKiB, flags); err != nil {
		if err2 := l.DomainSetMemory(dom, memKiB); err2 != nil {
			return fmt.Errorf("设置内存失败（对应 virsh setmem）: %w", err2)
		}
	}
	return nil
}
