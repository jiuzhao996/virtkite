package virt

import (
	"encoding/xml"
	"fmt"

	"github.com/digitalocean/go-libvirt"
)

// DomainInfo 域运行信息摘要（对应 virsh domstats 的基础项）。
type DomainInfo struct {
	State     string `json:"state"`
	MaxMemKiB uint64 `json:"max_mem_kib"`
	MemKiB    uint64 `json:"mem_kib"`
	VCPUs     uint64 `json:"vcpus"`
	CPUTimeNS uint64 `json:"cpu_time_ns"`
}

// Device 域设备摘要（磁盘/网卡，来自 XML 解析）。
type Device struct {
	Type     string `json:"type"`
	Target   string `json:"target"`
	Source   string `json:"source"`
	Model    string `json:"model,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// GetDomainInfo 返回域运行信息（对应 virsh domstats）。
// 未运行/失败时返回空信息与错误。
func (v *Virt) GetDomainInfo(name string) (DomainInfo, error) {
	l, err := v.getConn()
	if err != nil {
		return DomainInfo{}, err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return DomainInfo{}, fmt.Errorf("虚拟机 %s 不存在: %w", name, err)
	}
	state, maxMem, memory, vcpus, cpuTime, err := l.DomainGetInfo(dom)
	if err != nil {
		return DomainInfo{}, fmt.Errorf("获取虚拟机运行信息失败: %w", err)
	}
	return DomainInfo{
		State:     StateToPlatform(int32(state)),
		MaxMemKiB: maxMem,
		MemKiB:    memory,
		VCPUs:     uint64(vcpus),
		CPUTimeNS: cpuTime,
	}, nil
}

// GetMemoryStats 返回域 balloon 内存统计（对应 virsh dommemstat），单位 KiB。
// 键：actual（客户机总内存）/ unused / available / rss / usable。
// 无 balloon 驱动或失败时返回空 map（调用方回退到分配内存口径）。
func (v *Virt) GetMemoryStats(name string) map[string]uint64 {
	l, err := v.getConn()
	if err != nil {
		return nil
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return nil
	}
	stats, err := l.DomainMemoryStats(dom, 10, 0)
	if err != nil {
		return nil
	}
	out := make(map[string]uint64, len(stats))
	for _, s := range stats {
		switch libvirt.DomainMemoryStatTags(s.Tag) {
		case libvirt.DomainMemoryStatUnused:
			out["unused"] = s.Val
		case libvirt.DomainMemoryStatAvailable:
			out["available"] = s.Val
		case libvirt.DomainMemoryStatActualBalloon:
			out["actual"] = s.Val
		case libvirt.DomainMemoryStatRss:
			out["rss"] = s.Val
		case libvirt.DomainMemoryStatUsable:
			out["usable"] = s.Val
		}
	}
	return out
}

// ListDomainDevices 解析 domain XML，返回磁盘与网卡摘要。
// 用于 VM 详情页展示（对应 virsh domblklist / domiflist）。
func (v *Virt) ListDomainDevices(name string) (disks []Device, nics []Device, err error) {
	xmlstr, err := v.GetDomainXML(name)
	if err != nil {
		return nil, nil, err
	}

	var dx struct {
		Devices struct {
			Disks []struct {
				Type   string `xml:"type,attr"`
				Device string `xml:"device,attr"`
				Driver struct {
					Type string `xml:"type,attr"`
				} `xml:"driver"`
				Source struct {
					File string `xml:"file,attr"`
					Dev  string `xml:"dev,attr"`
				} `xml:"source"`
				Target struct {
					Dev string `xml:"dev,attr"`
				} `xml:"target"`
				ReadOnly bool `xml:"readonly"`
			} `xml:"disk"`
			Interfaces []struct {
				Type   string `xml:"type,attr"`
				Source struct {
					Network string `xml:"network,attr"`
					Bridge  string `xml:"bridge,attr"`
				} `xml:"source"`
				MAC struct {
					Address string `xml:"address,attr"`
				} `xml:"mac"`
				Model struct {
					Type string `xml:"type,attr"`
				} `xml:"model"`
			} `xml:"interface"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(xmlstr), &dx); err != nil {
		return nil, nil, fmt.Errorf("解析虚拟机 XML 失败: %w", err)
	}

	for _, d := range dx.Devices.Disks {
		src := d.Source.File
		if src == "" {
			src = d.Source.Dev
		}
		disks = append(disks, Device{
			Type:     d.Device, // disk / cdrom
			Target:   d.Target.Dev,
			Source:   src,
			Model:    d.Driver.Type,
			ReadOnly: d.ReadOnly,
		})
	}
	for _, n := range dx.Devices.Interfaces {
		src := n.Source.Network
		if src == "" {
			src = n.Source.Bridge
		}
		nics = append(nics, Device{
			Type:   n.Type, // network / bridge / direct
			Target: n.MAC.Address,
			Source: src,
			Model:  n.Model.Type,
		})
	}
	return disks, nics, nil
}
