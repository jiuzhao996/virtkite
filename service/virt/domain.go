package virt

import (
	"encoding/xml"
	"fmt"

	"github.com/digitalocean/go-libvirt"
)

// DefineDomain 定义虚拟机域（对应 virsh define，不启动）。
func (v *Virt) DefineDomain(xml string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	if _, err := l.DomainDefineXML(xml); err != nil {
		return fmt.Errorf("virsh define 等价调用失败: %v", err)
	}
	return nil
}

// StartDomain 启动虚拟机（对应 virsh start）。
func (v *Virt) StartDomain(name string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %v", name, err)
	}
	if err := l.DomainCreate(dom); err != nil {
		return fmt.Errorf("启动虚拟机失败: %v", err)
	}
	return nil
}

// ShutdownDomain 优雅关机（对应 virsh shutdown，发送 ACPI 关机信号）。
func (v *Virt) ShutdownDomain(name string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %v", name, err)
	}
	if err := l.DomainShutdown(dom); err != nil {
		return fmt.Errorf("关机失败: %v", err)
	}
	return nil
}

// RebootDomain 重启虚拟机（对应 virsh reboot，需已运行）。
func (v *Virt) RebootDomain(name string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %v", name, err)
	}
	if err := l.DomainReboot(dom, libvirt.DomainRebootDefault); err != nil {
		return fmt.Errorf("重启失败: %v", err)
	}
	return nil
}

// DestroyDomain 强制关闭虚拟机（对应 virsh destroy，立即断电）。
func (v *Virt) DestroyDomain(name string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %v", name, err)
	}
	if err := l.DomainDestroy(dom); err != nil {
		return fmt.Errorf("强制关闭失败: %v", err)
	}
	return nil
}

// UndefineDomain 删除虚拟机定义（对应 virsh undefine，不删除存储卷）。
// 若域正在运行，先强制销毁（virsh destroy）再删除定义（virsh undefine）。
func (v *Virt) UndefineDomain(name string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %v", name, err)
	}

	state, _, err := l.DomainGetState(dom, 0)
	if err != nil {
		return fmt.Errorf("获取虚拟机状态失败: %v", err)
	}
	if libvirt.DomainState(state) == libvirt.DomainRunning {
		if err := l.DomainDestroy(dom); err != nil {
			return fmt.Errorf("强制关闭虚拟机失败: %v", err)
		}
	}

	if err := l.DomainUndefine(dom); err != nil {
		return fmt.Errorf("删除虚拟机定义失败: %v", err)
	}
	return nil
}

// GetDomainState 获取虚拟机实时状态（返回平台 status 字符串）。
func (v *Virt) GetDomainState(name string) (string, error) {
	l, err := v.getConn()
	if err != nil {
		return "", err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return "", fmt.Errorf("虚拟机 %s 不存在: %v", name, err)
	}
	state, _, err := l.DomainGetState(dom, 0)
	if err != nil {
		return "", fmt.Errorf("获取虚拟机状态失败: %v", err)
	}
	return StateToPlatform(state), nil
}

// GetVNCInfo 返回运行中虚拟机的 VNC 端口（解析 graphics XML）。
// VM 未运行或无 VNC 配置时返回错误。
func (v *Virt) GetVNCInfo(name string) (int, error) {
	l, err := v.getConn()
	if err != nil {
		return 0, err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return 0, fmt.Errorf("虚拟机 %s 不存在: %v", name, err)
	}

	state, _, err := l.DomainGetState(dom, 0)
	if err != nil {
		return 0, fmt.Errorf("获取虚拟机状态失败: %v", err)
	}
	if libvirt.DomainState(state) != libvirt.DomainRunning {
		return 0, fmt.Errorf("虚拟机 %s 未运行，无法连接控制台", name)
	}

	xmlstr, err := l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return 0, fmt.Errorf("获取虚拟机 XML 失败: %v", err)
	}

	var d struct {
		Devices struct {
			Graphics []struct {
				Type string `xml:"type,attr"`
				Port int    `xml:"port,attr"`
			} `xml:"graphics"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(xmlstr), &d); err != nil {
		return 0, fmt.Errorf("解析虚拟机 XML 失败: %v", err)
	}
	for _, g := range d.Devices.Graphics {
		if g.Type == "vnc" && g.Port > 0 {
			return g.Port, nil
		}
	}
	return 0, fmt.Errorf("虚拟机 %s 未配置 VNC", name)
}

// GetAllDomainStates 返回所有域的名称与平台状态映射（对应 virsh list --all + domstate）。
func (v *Virt) GetAllDomainStates() (map[string]string, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}

	flags := libvirt.ConnectListDomainsActive | libvirt.ConnectListDomainsInactive
	domains, _, err := l.ConnectListAllDomains(1, flags)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(domains))
	for _, d := range domains {
		state, _, err := l.DomainGetState(d, 0)
		if err != nil {
			continue
		}
		result[d.Name] = StateToPlatform(state)
	}
	return result, nil
}

// GetDomainXML 返回虚拟机完整 XML 定义（对应 virsh dumpxml）。
func (v *Virt) GetDomainXML(name string) (string, error) {
	l, err := v.getConn()
	if err != nil {
		return "", err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return "", fmt.Errorf("虚拟机 %s 不存在: %v", name, err)
	}
	xmlstr, err := l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return "", fmt.Errorf("获取虚拟机 XML 失败: %v", err)
	}
	return xmlstr, nil
}

// UpdateDomainXML 更新虚拟机 XML 定义（对应 virsh edit）。
// 通过重新 define 实现：先取旧 XML 比对名称，再 define 新 XML。
// 注意：若 VM 正在运行，需先关机才能修改大部分配置。
func (v *Virt) UpdateDomainXML(name, newXML string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}

	// 校验新 XML 中名称与目标一致
	var dom struct {
		Name string `xml:"name"`
	}
	if err := xml.Unmarshal([]byte(newXML), &dom); err != nil {
		return fmt.Errorf("XML 解析失败: %v", err)
	}
	if dom.Name != name {
		return fmt.Errorf("XML 中名称 %s 与目标 %s 不一致", dom.Name, name)
	}

	if _, err := l.DomainDefineXML(newXML); err != nil {
		return fmt.Errorf("更新虚拟机定义失败: %v", err)
	}
	return nil
}