package virt

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// DiskSpec 磁盘设备（对应 virsh dumpxml 的 <disk> 元素）。
type DiskSpec struct {
	Type        string `json:"type"`                   // file / block
	Device      string `json:"device"`                 // disk / cdrom / floppy
	Driver      string `json:"driver"`                 // qcow2 / raw / iso
	Bus         string `json:"bus"`                    // virtio / ide / sata / scsi
	Source      string `json:"source"`                 // 文件路径或块设备路径
	Target      string `json:"target"`                 // vda / hda / sda（自动分配）
	ReadOnly    bool   `json:"read_only"`              // 只读（cdrom 一般为 true）
	BackingFile string `json:"backing_file,omitempty"` // 增量克隆父盘（展示用，不参与 XML）
}

// InterfaceSpec 网卡（对应 virsh dumpxml 的 <interface> 元素）。
type InterfaceSpec struct {
	Type   string `json:"type"`   // network / bridge / direct
	Source string `json:"source"` // 网络名或桥名
	MAC    string `json:"mac"`    // 网卡 MAC 地址
	Model  string `json:"model"`  // virtio / e1000 / rtl8139
}

// GraphicsSpec 显示设备（当前仅支持 VNC）。
type GraphicsSpec struct {
	Type string `json:"type"` // vnc
	Port int    `json:"port"` // -1 表示 autoport
}

// BootSpec 引导顺序（hd / cdrom / network）。
type BootSpec struct {
	Devices []string `json:"devices"`
}

// DomainSpec 域完整定义（VM 配置模型，对应 virsh dumpxml 的结构化视图）。
// 注意：CloudInitSpec 类型已由 cloudinit.go 定义（契约要求的字段完全一致），此处不再重复声明。
type DomainSpec struct {
	Name       string          `json:"name"`
	UUID       string          `json:"uuid"`
	VCPU       int             `json:"vcpu"`
	MemoryMB   int             `json:"memory_mb"`
	OSType     string          `json:"os_type"`
	Arch       string          `json:"arch"`
	Machine    string          `json:"machine"`
	Boot       BootSpec        `json:"boot"`
	Disks      []DiskSpec      `json:"disks"`
	Interfaces []InterfaceSpec `json:"interfaces"`
	Graphics   GraphicsSpec    `json:"graphics"`
	Autostart  bool            `json:"autostart"`
	CloudInit  *CloudInitSpec  `json:"cloud_init,omitempty"`
	RawXML     string          `json:"raw_xml,omitempty"` // dumpxml 原文（编辑回显）
}

// 以下为 encoding/xml 序列化用结构（命名类型，供 BuildDomainXML 与单设备片段共用）。

// emptyXML 用于生成 <acpi/>、<readonly/> 等空元素。
type emptyXML struct{}

type diskDriverXML struct {
	Name string `xml:"name,attr"`
	Type string `xml:"type,attr"`
}

type diskSourceXML struct {
	File string `xml:"file,attr,omitempty"`
	Dev  string `xml:"dev,attr,omitempty"`
}

type diskTargetXML struct {
	Dev string `xml:"dev,attr"`
	Bus string `xml:"bus,attr,omitempty"`
}

type diskXML struct {
	XMLName  xml.Name       `xml:"disk"`
	Type     string         `xml:"type,attr"`
	Device   string         `xml:"device,attr"`
	Driver   *diskDriverXML `xml:"driver"`
	Source   *diskSourceXML `xml:"source"`
	Target   diskTargetXML  `xml:"target"`
	ReadOnly *emptyXML      `xml:"readonly"`
}

type interfaceMacXML struct {
	Address string `xml:"address,attr"`
}

type interfaceSourceXML struct {
	Network string `xml:"network,attr,omitempty"`
	Bridge  string `xml:"bridge,attr,omitempty"`
	Dev     string `xml:"dev,attr,omitempty"`
}

type interfaceModelXML struct {
	Type string `xml:"type,attr"`
}

type interfaceXML struct {
	XMLName xml.Name            `xml:"interface"`
	Type    string              `xml:"type,attr"`
	MAC     *interfaceMacXML    `xml:"mac"`
	Source  *interfaceSourceXML `xml:"source"`
	Model   *interfaceModelXML  `xml:"model"`
}

type memoryXML struct {
	Unit  string `xml:"unit,attr"`
	Value uint64 `xml:",chardata"`
}

type vcpuXML struct {
	Placement string `xml:"placement,attr"`
	Value     int    `xml:",chardata"`
}

type osTypeXML struct {
	Arch    string `xml:"arch,attr,omitempty"`
	Machine string `xml:"machine,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type bootXML struct {
	Dev string `xml:"dev,attr"`
}

type osXML struct {
	Type osTypeXML `xml:"type"`
	Boot []bootXML `xml:"boot"`
}

type featuresXML struct {
	ACPI *emptyXML `xml:"acpi"`
	APIC *emptyXML `xml:"apic"`
}

type graphicsXML struct {
	Type     string `xml:"type,attr"`
	Port     int    `xml:"port,attr"`
	AutoPort string `xml:"autoport,attr,omitempty"`
}

type devicesXML struct {
	Disks      []diskXML      `xml:"disk"`
	Interfaces []interfaceXML `xml:"interface"`
	Graphics   graphicsXML    `xml:"graphics"`
}

type domainSpecXML struct {
	XMLName  xml.Name    `xml:"domain"`
	Type     string      `xml:"type,attr"`
	Name     string      `xml:"name"`
	UUID     string      `xml:"uuid,omitempty"`
	Memory   memoryXML   `xml:"memory"`
	VCPU     vcpuXML     `xml:"vcpu"`
	OS       osXML       `xml:"os"`
	Features featuresXML `xml:"features"`
	Devices  devicesXML  `xml:"devices"`
}

// xmlMarshal 封装 encoding/xml 序列化，返回字符串（内部生成 XML 共用）。
func xmlMarshal(v interface{}) (string, error) {
	b, err := xml.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// diskDriverType 返回磁盘驱动类型：spec.Driver 为空时 cdrom 用 raw，其余默认 qcow2。
func diskDriverType(d DiskSpec) string {
	if d.Driver != "" {
		return d.Driver
	}
	if d.Device == "cdrom" {
		return "raw"
	}
	return "qcow2"
}

// diskXMLFromSpec 将 DiskSpec 转为 XML 结构（生成单设备片段与完整 domain XML 共用）。
func diskXMLFromSpec(d DiskSpec) diskXML {
	dx := diskXML{Type: d.Type, Device: d.Device}
	if dx.Type == "" {
		dx.Type = "file"
	}
	if dx.Device == "" {
		dx.Device = "disk"
	}
	dx.Driver = &diskDriverXML{Name: "qemu", Type: diskDriverType(d)}
	if d.Source != "" {
		src := diskSourceXML{}
		if d.Type == "block" {
			src.Dev = d.Source
		} else {
			src.File = d.Source
		}
		dx.Source = &src
	}
	dx.Target = diskTargetXML{Dev: d.Target, Bus: d.Bus}
	if d.ReadOnly {
		dx.ReadOnly = &emptyXML{}
	}
	return dx
}

// interfaceXMLFromSpec 将 InterfaceSpec 转为 XML 结构（生成单设备片段与完整 domain XML 共用）。
func interfaceXMLFromSpec(i InterfaceSpec) interfaceXML {
	ifx := interfaceXML{Type: i.Type}
	if ifx.Type == "" {
		ifx.Type = "network"
	}
	if i.MAC != "" {
		ifx.MAC = &interfaceMacXML{Address: i.MAC}
	}
	switch ifx.Type {
	case "bridge":
		ifx.Source = &interfaceSourceXML{Bridge: i.Source}
	case "direct":
		ifx.Source = &interfaceSourceXML{Dev: i.Source}
	default:
		ifx.Source = &interfaceSourceXML{Network: i.Source}
	}
	model := i.Model
	if model == "" {
		model = "virtio"
	}
	ifx.Model = &interfaceModelXML{Type: model}
	return ifx
}

// GetDomainSpec 返回虚拟机完整配置模型（对应 virsh dumpxml + autostart）。
// autostart 不属于 domain XML，需单独通过 DomainGetAutostart 查询（对应 virsh dominfo）。
func (v *Virt) GetDomainSpec(name string) (*DomainSpec, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return nil, fmt.Errorf("虚拟机 %s 不存在: %w", name, err)
	}
	xmlstr, err := l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return nil, fmt.Errorf("获取虚拟机 XML 失败（对应 virsh dumpxml）: %w", err)
	}
	spec, err := ParseDomainXML(xmlstr)
	if err != nil {
		return nil, err
	}
	spec.RawXML = xmlstr
	autostart, err := l.DomainGetAutostart(dom)
	if err == nil {
		spec.Autostart = autostart == 1
	}
	return spec, nil
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
			Value string `xml:",chardata"`
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
				ReadOnly *emptyXML `xml:"readonly"`
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
		VCPU:     parseInt(dx.VCPU.Value),
		MemoryMB: memoryToMB(dx.Memory.Value, dx.Memory.Unit),
		OSType:   dx.OS.Type.Value,
		Arch:     dx.OS.Type.Arch,
		Machine:  dx.OS.Type.Machine,
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
			Type:     d.Type,
			Device:   d.Device,
			Driver:   d.Driver.Type,
			Bus:      d.Target.Bus,
			Source:   src,
			Target:   d.Target.Dev,
			ReadOnly: d.ReadOnly != nil,
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

// BuildDomainXML 根据 DomainSpec 生成完整 domain XML（对应 virsh define 的输入）。
// 生成含 <memory unit='KiB'>、<vcpu placement='static'>、acpi/apic features、
// graphics vnc（端口 -1 表示 autoport）、按 Boot.Devices 的 <os><boot dev=.../>、
// 磁盘与网卡。CloudInit 场景由调用方预先在 spec.Disks 末尾追加 seed 盘，此处正常输出全部 Disks。
func BuildDomainXML(spec *DomainSpec) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("DomainSpec 不能为 nil")
	}
	if spec.Name == "" {
		return "", fmt.Errorf("虚拟机名称不能为空")
	}
	if spec.VCPU <= 0 {
		return "", fmt.Errorf("vCPU 数量必须大于 0")
	}
	if spec.MemoryMB <= 0 {
		return "", fmt.Errorf("内存大小必须大于 0")
	}

	dx := domainSpecXML{Type: "kvm", Name: spec.Name, UUID: spec.UUID}
	dx.Memory = memoryXML{Unit: "KiB", Value: uint64(spec.MemoryMB) * 1024}
	dx.VCPU = vcpuXML{Placement: "static", Value: spec.VCPU}

	ostype := spec.OSType
	if ostype == "" {
		ostype = "hvm"
	}
	arch := spec.Arch
	if arch == "" {
		arch = "x86_64"
	}
	dx.OS.Type = osTypeXML{Arch: arch, Machine: spec.Machine, Value: ostype}

	boots := spec.Boot.Devices
	if len(boots) == 0 {
		boots = []string{"hd"}
	}
	for _, dev := range boots {
		dx.OS.Boot = append(dx.OS.Boot, bootXML{Dev: dev})
	}

	dx.Features = featuresXML{ACPI: &emptyXML{}, APIC: &emptyXML{}}

	for _, d := range spec.Disks {
		dx.Devices.Disks = append(dx.Devices.Disks, diskXMLFromSpec(d))
	}
	for _, i := range spec.Interfaces {
		dx.Devices.Interfaces = append(dx.Devices.Interfaces, interfaceXMLFromSpec(i))
	}

	gtype := spec.Graphics.Type
	if gtype == "" {
		gtype = "vnc"
	}
	g := graphicsXML{Type: gtype, Port: spec.Graphics.Port}
	if g.Port == -1 {
		g.AutoPort = "yes"
	}
	dx.Devices.Graphics = g

	out, err := xml.Marshal(dx)
	if err != nil {
		return "", fmt.Errorf("生成 domain XML 失败: %w", err)
	}
	return string(out), nil
}

// NextDiskTarget 按 bus 自动分配下一个磁盘 target 名（对应 virsh attach-disk 的 target 自动编号）。
// virtio → vda/vdb...，sata/scsi → sda/sdb...，ide → hda/hdb...；跳过已占用编号。
func NextDiskTarget(spec *DomainSpec, bus string) string {
	prefix := "vd"
	switch bus {
	case "sata", "scsi":
		prefix = "sd"
	case "ide":
		prefix = "hd"
	}

	used := make(map[int]bool)
	for _, d := range spec.Disks {
		if d.Bus != bus || len(d.Target) <= len(prefix) || d.Target[:len(prefix)] != prefix {
			continue
		}
		if idx := diskSuffixIndex(d.Target[len(prefix):]); idx >= 0 {
			used[idx] = true
		}
	}
	for i := 0; i < 26; i++ {
		if !used[i] {
			return prefix + string(rune('a'+i))
		}
	}
	// 超过 26 块时按 aa/ab 续编（极少见）
	return prefix + "aa"
}

// diskSuffixIndex 将 target 后缀（如 "a"、"ab"）换算为序号（a=0, b=1, ..., z=25, aa=26）。
func diskSuffixIndex(suffix string) int {
	if suffix == "" {
		return -1
	}
	idx := 0
	for _, r := range suffix {
		if r < 'a' || r > 'z' {
			return -1
		}
		idx = idx*26 + int(r-'a')
	}
	return idx
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
