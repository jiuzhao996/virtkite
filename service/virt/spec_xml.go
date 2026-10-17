// spec_xml.go：DomainSpec → libvirt domain XML 的 encoding/xml 序列化结构定义（schema 部分）。
// 各 struct 的 xml tag 即生成的元素名/属性名；构造逻辑见 spec_build.go，回读解析见 spec_parse.go。
package virt

import (
	"encoding/xml"
)

// 以下为 encoding/xml 序列化用结构（命名类型，供 BuildDomainXML 与单设备片段共用）。
// 每个 struct 的 `xml:"..."` tag 决定生成的元素名/属性名，等于 libvirt domain XML 的一部分。

// emptyXML 用于生成 <acpi/>、<readonly/> 等空元素（只有开始/结束标签、无属性无文本）。
type emptyXML struct{}

// diskDriverXML 磁盘后端驱动 <driver name='qemu' type='qcow2'/>。
// name 恒为 qemu（KVM 下唯一用户态块层）；type 是**镜像格式**（qcow2/raw/…），
// 写错会让 guest 按错误格式解析镜像而启动失败，故一律由 diskDriverType 统一推导。
type diskDriverXML struct {
	Name string `xml:"name,attr"`
	Type string `xml:"type,attr"`
}

// diskSourceXML 磁盘数据来源：file 属性给文件型镜像路径，dev 属性给块设备路径
// （qcow2/raw 文件走 file，LVM 逻辑卷/裸设备走 dev）。二者互斥，按 <disk type> 二选一。
type diskSourceXML struct {
	File string `xml:"file,attr,omitempty"`
	Dev  string `xml:"dev,attr,omitempty"`
}

// diskTargetXML 设备在 guest 内的落点 <target dev='vda' bus='virtio'/>。
// dev 是 guest 里看到的盘符（vda/sda/hda），bus 是挂载总线；二者共同决定 guest 内核如何识别该盘。
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

// interfaceSourceXML 网卡接入点，具体属性由 <interface type> 决定：
// network 属性给 libvirt 命名网络（virtio 网络名）；bridge 属性给宿主网桥名；
// dev 属性给物理网卡名（仅 direct/macvtap 用），此时还需 mode 指定 macvtap 工作模式。
type interfaceSourceXML struct {
	Network string `xml:"network,attr,omitempty"`
	Bridge  string `xml:"bridge,attr,omitempty"`
	Dev     string `xml:"dev,attr,omitempty"`
	// Mode 仅 direct（macvtap）用：bridge / private / vepa / passthrough
	Mode string `xml:"mode,attr,omitempty"`
}

// interfaceModelXML 网卡型号 <model type='virtio'/>。
// virtio 是半虚拟化网卡，吞吐/延迟远优于 e1000/rtl8139 的纯模拟；
// 仅当 guest 缺 virtio 驱动（如极老系统）时才退化用后者。
type interfaceModelXML struct {
	Type string `xml:"type,attr"`
}

// interfaceXML 网卡设备 <interface type='network'>，是 domain XML <devices> 的子节点。
// type 取值 network（接 libvirt 命名网络）/ bridge（接宿主网桥）/ direct（macvtap 直连物理网卡）。
type interfaceXML struct {
	XMLName xml.Name            `xml:"interface"`
	Type    string              `xml:"type,attr"`
	MAC     *interfaceMacXML    `xml:"mac"`
	Source  *interfaceSourceXML `xml:"source"`
	Model   *interfaceModelXML  `xml:"model"`
}

// memoryXML 启动内存 <memory unit='KiB'>N</memory>。
// unit 与数值必须配套：本项目统一按 KiB 下发（MemoryMB×1024），
// 若换 unit 却忘换算量级，内存会差出 1024 倍。
type memoryXML struct {
	Unit  string `xml:"unit,attr"`
	Value uint64 `xml:",chardata"`
}

// vcpuXML 虚拟 CPU 定义 <vcpu placement='static' current='n'>max</vcpu>。
// chardata 是 vCPU 上限（含热升级头寸），current 属性是开机当前核数；
// placement='static' 表示 vCPU 静态落在物理核上、不随调度漂移。
type vcpuXML struct {
	Placement string `xml:"placement,attr"`
	Current   int    `xml:"current,attr,omitempty"`
	Value     int    `xml:",chardata"`
}

// maxMemoryXML 内存热插拔上限 <maxMemory unit='KiB'>N</maxMemory>。
// 仅是「最多可热增到多少」的天花板，不实际占用内存（balloon/热插用）。
type maxMemoryXML struct {
	Unit  string `xml:"unit,attr"`
	Value uint64 `xml:",chardata"`
}

// osTypeXML guest 操作系统类型 <type arch='x86_64' machine='q35'>hvm</type>。
// hvm = 硬件辅助全虚拟化（KVM 场景的标准值）；machine 指定芯片组型号（q35 为现代 PCIe 芯片组）。
type osTypeXML struct {
	Arch    string `xml:"arch,attr,omitempty"`
	Machine string `xml:"machine,attr,omitempty"`
	Value   string `xml:",chardata"`
}

// bootXML 单个引导设备 <boot dev='hd'/>；多个 <boot> 按出现顺序构成引导顺序（hd/cdrom/network）。
type bootXML struct {
	Dev string `xml:"dev,attr"`
}

type osXML struct {
	Type osTypeXML `xml:"type"`
	Boot []bootXML `xml:"boot"`
	// 直接内核引导（网络安装源 FTP/HTTP）：<kernel>/<initrd>/<cmdline>
	Kernel  string `xml:"kernel,omitempty"`
	Initrd  string `xml:"initrd,omitempty"`
	Cmdline string `xml:"cmdline,omitempty"`
}

// featuresXML <features> 下的 ACPI/APIC 开关。
// ACPI 打开后 guest 才能响应关机/重启等电源信号（否则优雅关机失效，只能 destroy 硬断电）；
// APIC 是中断控制器，多 vCPU 必需。
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

// graphicsXML 显示设备 <graphics type='vnc' port='-1' autoport='yes'/>。
// port=-1 配合 autoport='yes' 让 libvirt 自动挑一个空闲端口，对上层屏蔽端口冲突。
type graphicsXML struct {
	Type     string `xml:"type,attr"`
	Port     int    `xml:"port,attr"`
	AutoPort string `xml:"autoport,attr,omitempty"`
}

// devicesXML domain XML 的 <devices> 容器，聚合所有虚拟设备。
// 其中 Serial/Console 用 pty 后端，正是 `virsh console <vm>` 依赖的字符设备；
// Channels 是 Guest Agent 通道；libvirt 隐式默认也会补 memballoon 等，这里显式输出以求语义明确、与手工模板一致。
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
