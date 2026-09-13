package virt

import (
	"encoding/xml"
	"fmt"
)

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
	XMLName      xml.Name         `xml:"disk"`
	Type         string           `xml:"type,attr"`
	Device       string           `xml:"device,attr"`
	Driver       *diskDriverXML   `xml:"driver"`
	Source       *diskSourceXML   `xml:"source"`
	BackingStore *backingStoreXML `xml:"backingStore"` // 增量盘的父盘链（qcow2 backing）
	Target       diskTargetXML    `xml:"target"`
	ReadOnly     *emptyXML        `xml:"readonly"`
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
	Current   int    `xml:"current,attr,omitempty"`
	Value     int    `xml:",chardata"`
}

type maxMemoryXML struct {
	Unit  string `xml:"unit,attr"`
	Value uint64 `xml:",chardata"`
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

// cpuXML CPU 模型定义（host-passthrough 直通宿主 CPU，性能最好且嵌套虚拟化可用；
// libvirt 缺省的 custom 派生模型性能差，故平台默认显式输出直通）。
type cpuXML struct {
	Mode string `xml:"mode,attr"`
}

// timerXML <clock> 子定时器（与手工模板一致：rtc catchup / pit delay / hpet 关闭，抗时钟漂移）。
type timerXML struct {
	Name       string `xml:"name,attr"`
	TickPolicy string `xml:"tickpolicy,attr,omitempty"`
	Present    string `xml:"present,attr,omitempty"`
}

type clockXML struct {
	Offset string     `xml:"offset,attr"`
	Timers []timerXML `xml:"timer"`
}

// serialDevXML 串口/控制台设备（serial 与 console 共用同一形状，靠父级 tag 区分元素名）。
type serialDevXML struct {
	Type   string           `xml:"type,attr"`
	Target *serialTargetXML `xml:"target"`
}

type serialTargetXML struct {
	Type  string          `xml:"type,attr"`
	Port  int             `xml:"port,attr"`
	Model *serialModelXML `xml:"model,omitempty"`
}

type serialModelXML struct {
	Name string `xml:"name,attr"`
}

// channelXML Guest Agent 通道（org.qemu.guest_agent.0）：guest 内安装 qemu-guest-agent
// 后可经 virtio 串口通信，是后续走 agent 途径回填 IP/采集 guest 信息的前提。
// XMLName 必须显式声明：单设备片段独立序列化时（attach-device）靠它确定根元素名。
type channelXML struct {
	XMLName xml.Name         `xml:"channel"`
	Type    string           `xml:"type,attr"`
	Target  channelTargetXML `xml:"target"`
}

type channelTargetXML struct {
	Type string `xml:"type,attr"`
	Name string `xml:"name,attr"`
}

// rngXML virtio 随机数发生器：云镜像 guest 熵不足会导致启动慢/SSH 卡顿。
type rngXML struct {
	XMLName xml.Name      `xml:"rng"`
	Model   string        `xml:"model,attr"`
	Backend rngBackendXML `xml:"backend"`
}

type rngBackendXML struct {
	Model string `xml:"model,attr"`
	Value string `xml:",chardata"`
}

// memballoonXML 内存气球设备（libvirt 隐式默认也会加，显式输出保持与模板一致、语义明确）。
type memballoonXML struct {
	Model string `xml:"model,attr"`
}

type graphicsXML struct {
	Type     string `xml:"type,attr"`
	Port     int    `xml:"port,attr"`
	AutoPort string `xml:"autoport,attr,omitempty"`
}

type devicesXML struct {
	Disks      []diskXML      `xml:"disk"`
	Interfaces []interfaceXML `xml:"interface"`
	Serial     *serialDevXML  `xml:"serial,omitempty"`
	Console    *serialDevXML  `xml:"console,omitempty"`
	Channels   []channelXML   `xml:"channel,omitempty"`
	Rng        *rngXML        `xml:"rng,omitempty"`
	Memballoon *memballoonXML `xml:"memballoon,omitempty"`
	Graphics   graphicsXML    `xml:"graphics"`
}

type domainSpecXML struct {
	XMLName   xml.Name      `xml:"domain"`
	Type      string        `xml:"type,attr"`
	Name      string        `xml:"name"`
	UUID      string        `xml:"uuid,omitempty"`
	Memory    memoryXML     `xml:"memory"`
	MaxMemory *maxMemoryXML `xml:"maxMemory,omitempty"`
	VCPU      vcpuXML       `xml:"vcpu"`
	OS        osXML         `xml:"os"`
	Features  featuresXML   `xml:"features"`
	CPU       *cpuXML       `xml:"cpu,omitempty"`
	Clock     *clockXML     `xml:"clock,omitempty"`
	Devices   devicesXML    `xml:"devices"`
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

// vcpuMax 计算 vcpu 热升级头寸：max(2*n, n+4)，上限 64。
// 生成 <vcpu current='n'>max</vcpu>，运行中可热增到 max（热减不被 QEMU 支持，走关机）。
func vcpuMax(n int) int {
	max := n * 2
	if n+4 > max {
		max = n + 4
	}
	if max > 64 {
		max = 64
	}
	return max
}

// memoryMaxKiB 计算内存热升级头寸：max(2*cur, cur+2048MiB)。
// 生成 <maxMemory> 作为热插拔上限（仅预留上限，不实际占用），运行中可热增到该值。
func memoryMaxKiB(cur int) uint64 {
	maxMB := cur * 2
	if cur+2048 > maxMB {
		maxMB = cur + 2048
	}
	return uint64(maxMB) * 1024
}

// BuildDomainXML 根据 DomainSpec 生成完整 domain XML（对应 virsh define 的输入）。
// 生成含 <memory unit='KiB'>、<maxMemory>（热插拔上限）、<vcpu placement='static' current='n'>max</vcpu>
// （当前核数 n，上限含热升级头寸）、acpi/apic features、graphics vnc（端口 -1 表示 autoport）、
// 按 Boot.Devices 的 <os><boot dev=.../>、磁盘与网卡。
// CloudInit 场景由调用方预先在 spec.Disks 末尾追加 seed 盘，此处正常输出全部 Disks。
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
	dx.MaxMemory = &maxMemoryXML{Unit: "KiB", Value: memoryMaxKiB(spec.MemoryMB)}
	dx.VCPU = vcpuXML{Placement: "static", Current: spec.VCPU, Value: vcpuMax(spec.VCPU)}

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

	// CPU 直通：与手工模板一致（host-passthrough），性能最好且嵌套虚拟化可用；
	// CPUMode 为空也按直通处理（libvirt 缺省 custom 派生模型性能差）；
	// "default" 表示显式不输出 cpu 节点、保留 libvirt 缺省语义。
	cpuMode := spec.CPUMode
	if cpuMode == "" {
		cpuMode = "host-passthrough"
	}
	if cpuMode != "default" {
		dx.CPU = &cpuXML{Mode: cpuMode}
	}

	// 时钟：UTC + 定时器策略（rtc catchup / pit delay / hpet 关闭），抗时钟漂移
	dx.Clock = &clockXML{Offset: "utc", Timers: []timerXML{
		{Name: "rtc", TickPolicy: "catchup"},
		{Name: "pit", TickPolicy: "delay"},
		{Name: "hpet", Present: "no"},
	}}

	for _, d := range spec.Disks {
		dx.Devices.Disks = append(dx.Devices.Disks, diskXMLFromSpec(d))
	}
	for _, i := range spec.Interfaces {
		dx.Devices.Interfaces = append(dx.Devices.Interfaces, interfaceXMLFromSpec(i))
	}

	// 串口 + 控制台：显式声明 pty（对应 virsh console 依赖的设备），不依赖 libvirt 隐式默认
	dx.Devices.Serial = &serialDevXML{
		Type:   "pty",
		Target: &serialTargetXML{Type: "isa-serial", Port: 0, Model: &serialModelXML{Name: "isa-serial"}},
	}
	dx.Devices.Console = &serialDevXML{
		Type:   "pty",
		Target: &serialTargetXML{Type: "serial", Port: 0},
	}
	// Guest Agent 通道 + virtio-rng + 内存气球（与手工模板对齐）
	dx.Devices.Channels = []channelXML{{
		Type:   "unix",
		Target: channelTargetXML{Type: "virtio", Name: "org.qemu.guest_agent.0"},
	}}
	dx.Devices.Rng = &rngXML{Model: "virtio", Backend: rngBackendXML{Model: "random", Value: "/dev/urandom"}}
	dx.Devices.Memballoon = &memballoonXML{Model: "virtio"}

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
