package virt

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// backingStoreXML 增量盘父盘（可多层嵌套：增量盘的父盘也可能是增量盘）。
// 解析与生成两侧共用（diskXML 的 BackingStore 字段、ParseDomainXML 的回读）。
type backingStoreXML struct {
	Type         string           `xml:"type,attr"`
	Source       *diskSourceXML   `xml:"source"`
	BackingStore *backingStoreXML `xml:"backingStore"`
}

// chain 返回父盘链的可读形式："base/Rocky.img"，多层用 " ← " 连接（子 ← 父）。
func (b *backingStoreXML) chain() string {
	if b == nil || b.Source == nil {
		return ""
	}
	parent := b.Source.File
	if parent == "" {
		parent = b.Source.Dev
	}
	if rest := b.BackingStore.chain(); rest != "" {
		return parent + " ← " + rest
	}
	return parent
}

// ParseDomainXML 将 virsh dumpxml 返回的完整 domain XML 解析为 DomainSpec。
// 纯函数，不依赖 libvirt 连接。内存按 unit 属性换算为 MB（默认/未声明按 KiB，另支持 MiB/GiB）。
// 注意：features（acpi/apic）由 BuildDomainXML 固定生成，此处无需回填；
// autostart 不在 domain XML 中，由 GetDomainSpec 单独查询。
func ParseDomainXML(xmlstr string) (*DomainSpec, error) {
	var dx struct {
		Name   string `xml:"name"`
		UUID   string `xml:"uuid"`
		Memory struct {
			Unit  string `xml:"unit,attr"`
			Value string `xml:",chardata"`
		} `xml:"memory"`
		VCPU struct {
			Current string `xml:"current,attr"`
			Value   string `xml:",chardata"`
		} `xml:"vcpu"`
		OS struct {
			Type struct {
				Arch    string `xml:"arch,attr"`
				Machine string `xml:"machine,attr"`
				Value   string `xml:",chardata"`
			} `xml:"type"`
			Boot []struct {
				Dev string `xml:"dev,attr"`
			} `xml:"boot"`
		} `xml:"os"`
		CPU struct {
			Mode string `xml:"mode,attr"`
		} `xml:"cpu"`
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
					Bus string `xml:"bus,attr"`
				} `xml:"target"`
				BackingStore *backingStoreXML `xml:"backingStore"`
				ReadOnly     *emptyXML        `xml:"readonly"`
			} `xml:"disk"`
			Interfaces []struct {
				Type   string `xml:"type,attr"`
				Source struct {
					Network string `xml:"network,attr"`
					Bridge  string `xml:"bridge,attr"`
					Dev     string `xml:"dev,attr"`
				} `xml:"source"`
				MAC struct {
					Address string `xml:"address,attr"`
				} `xml:"mac"`
				Model struct {
					Type string `xml:"type,attr"`
				} `xml:"model"`
			} `xml:"interface"`
			Graphics []struct {
				Type string `xml:"type,attr"`
				Port int    `xml:"port,attr"`
			} `xml:"graphics"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(xmlstr), &dx); err != nil {
		return nil, fmt.Errorf("解析 domain XML 失败: %w", err)
	}

	spec := &DomainSpec{
		Name:     dx.Name,
		UUID:     dx.UUID,
		VCPU:     parseInt(dx.VCPU.Current), // current 属性为当前核数（热升级头寸存于 chardata）
		MemoryMB: memoryToMB(dx.Memory.Value, dx.Memory.Unit),
		OSType:   dx.OS.Type.Value,
		Arch:     dx.OS.Type.Arch,
		Machine:  dx.OS.Type.Machine,
		CPUMode:  dx.CPU.Mode,
	}
	if spec.VCPU == 0 {
		spec.VCPU = parseInt(dx.VCPU.Value)
	}
	for _, b := range dx.OS.Boot {
		spec.Boot.Devices = append(spec.Boot.Devices, b.Dev)
	}
	for _, d := range dx.Devices.Disks {
		src := d.Source.File
		if src == "" {
			src = d.Source.Dev
		}
		spec.Disks = append(spec.Disks, DiskSpec{
			Type:        d.Type,
			Device:      d.Device,
			Driver:      d.Driver.Type,
			Bus:         d.Target.Bus,
			Source:      src,
			Target:      d.Target.Dev,
			BackingFile: d.BackingStore.chain(),
			ReadOnly:    d.ReadOnly != nil,
		})
	}
	for _, n := range dx.Devices.Interfaces {
		src := n.Source.Network
		if src == "" {
			src = n.Source.Bridge
		}
		if src == "" {
			src = n.Source.Dev
		}
		spec.Interfaces = append(spec.Interfaces, InterfaceSpec{
			Type:   n.Type,
			Source: src,
			MAC:    n.MAC.Address,
			Model:  n.Model.Type,
		})
	}
	if len(dx.Devices.Graphics) > 0 {
		spec.Graphics = GraphicsSpec{
			Type: dx.Devices.Graphics[0].Type,
			Port: dx.Devices.Graphics[0].Port,
		}
	}
	return spec, nil
}

// parseInt 安全解析整数字符串（失败返回 0，容忍 XML 中的空白字符）。
func parseInt(s string) int {
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// memoryToMB 将 XML <memory> 的值按 unit 属性换算为 MB。
// libvirt 未声明 unit 时默认 KiB；dumpxml 常见 KiB/MiB，另有 GiB/bytes 兜底。
func memoryToMB(value, unit string) int {
	v := uint64(parseInt(value))
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "mib":
		return int(v)
	case "gib":
		return int(v * 1024)
	case "bytes", "b":
		return int(v / 1024 / 1024)
	default: // KiB 或未声明
		return int(v / 1024)
	}
}
