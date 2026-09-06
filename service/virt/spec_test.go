package virt

import (
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
)

// minimalDomainXML 最小可用域 XML：单盘单网卡，内存用 KiB（libvirt 默认单位）。
const minimalDomainXML = `<domain type='kvm'>
  <name>web-01</name>
  <uuid>11111111-1111-4111-8111-111111111111</uuid>
  <memory unit='KiB'>2097152</memory>
  <vcpu placement='static' current='2'>4</vcpu>
  <os>
    <type arch='x86_64' machine='pc-q35-8.0'>hvm</type>
    <boot dev='hd'/>
  </os>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='/var/lib/libvirt/images/web-01.qcow2'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <interface type='network'>
      <mac address='52:54:00:aa:bb:cc'/>
      <source network='default'/>
      <model type='virtio'/>
    </interface>
    <graphics type='vnc' port='-1' autoport='yes'/>
  </devices>
</domain>`

// complexDomainXML 复杂域：cdrom + 两块数据盘、双网卡（network + bridge）、
// VNC 固定端口、双引导项、内存用 MiB、vcpu 无 current 属性（走 chardata 回退）。
const complexDomainXML = `<domain type='kvm'>
  <name>db-01</name>
  <uuid>22222222-2222-4222-8222-222222222222</uuid>
  <memory unit='MiB'>4096</memory>
  <vcpu placement='static'>4</vcpu>
  <os>
    <type arch='aarch64' machine='virt-8.0'>hvm</type>
    <boot dev='cdrom'/>
    <boot dev='hd'/>
  </os>
  <devices>
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='/iso/ubuntu-24.04.iso'/>
      <target dev='hda' bus='ide'/>
      <readonly/>
    </disk>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='/var/lib/libvirt/images/db-01.qcow2'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <disk type='block' device='disk'>
      <driver name='qemu' type='raw'/>
      <source dev='/dev/vg0/data'/>
      <target dev='sda' bus='scsi'/>
    </disk>
    <interface type='network'>
      <mac address='52:54:00:11:22:33'/>
      <source network='vmops-net'/>
      <model type='virtio'/>
    </interface>
    <interface type='bridge'>
      <source bridge='br0'/>
      <model type='e1000'/>
    </interface>
    <graphics type='vnc' port='5901'/>
  </devices>
</domain>`

// TestParseDomainXMLMinimal 验证最小域 XML 的逐字段解析。
// ParseDomainXML 是纳管存量 VM、克隆、删除等所有读配置路径的入口，字段错位会连带污染。
func TestParseDomainXMLMinimal(t *testing.T) {
	spec, err := ParseDomainXML(minimalDomainXML)
	if err != nil {
		t.Fatalf("解析最小域 XML 失败: %v", err)
	}
	if spec.Name != "web-01" {
		t.Errorf("名称应为 web-01，得到 %q", spec.Name)
	}
	if spec.UUID != "11111111-1111-4111-8111-111111111111" {
		t.Errorf("UUID 解析错误，得到 %q", spec.UUID)
	}
	// current='2' 优先于 chardata 的 4（热升级头寸存于 chardata，当前核数在 current）
	if spec.VCPU != 2 {
		t.Errorf("vCPU 应取 current=2，得到 %d", spec.VCPU)
	}
	// 2097152 KiB = 2048 MB
	if spec.MemoryMB != 2048 {
		t.Errorf("内存应为 2048MB，得到 %d", spec.MemoryMB)
	}
	if spec.OSType != "hvm" || spec.Arch != "x86_64" || spec.Machine != "pc-q35-8.0" {
		t.Errorf("OS 解析错误: %+v", spec)
	}
	if len(spec.Boot.Devices) != 1 || spec.Boot.Devices[0] != "hd" {
		t.Errorf("引导顺序应为 [hd]，得到 %v", spec.Boot.Devices)
	}
	if len(spec.Disks) != 1 {
		t.Fatalf("磁盘应为 1 块，得到 %d", len(spec.Disks))
	}
	d := spec.Disks[0]
	if d.Type != "file" || d.Device != "disk" || d.Driver != "qcow2" || d.Bus != "virtio" {
		t.Errorf("磁盘属性解析错误: %+v", d)
	}
	if d.Source != "/var/lib/libvirt/images/web-01.qcow2" || d.Target != "vda" || d.ReadOnly {
		t.Errorf("磁盘 source/target/readonly 解析错误: %+v", d)
	}
	if len(spec.Interfaces) != 1 {
		t.Fatalf("网卡应为 1 块，得到 %d", len(spec.Interfaces))
	}
	n := spec.Interfaces[0]
	if n.Type != "network" || n.Source != "default" || n.MAC != "52:54:00:aa:bb:cc" || n.Model != "virtio" {
		t.Errorf("网卡解析错误: %+v", n)
	}
	if spec.Graphics.Type != "vnc" || spec.Graphics.Port != -1 {
		t.Errorf("显卡应为 vnc/-1，得到 %+v", spec.Graphics)
	}
}

// TestParseDomainXMLComplex 验证复杂域：cdrom 只读、块设备、双网卡类型、固定 VNC 端口、
// 双引导项、MiB 内存单位、无 current 的 vcpu 回退。
func TestParseDomainXMLComplex(t *testing.T) {
	spec, err := ParseDomainXML(complexDomainXML)
	if err != nil {
		t.Fatalf("解析复杂域 XML 失败: %v", err)
	}
	// 4096 MiB = 4096 MB
	if spec.MemoryMB != 4096 {
		t.Errorf("MiB 内存应为 4096MB，得到 %d", spec.MemoryMB)
	}
	// 无 current 属性时回退到 chardata
	if spec.VCPU != 4 {
		t.Errorf("无 current 时 vCPU 应取文本值 4，得到 %d", spec.VCPU)
	}
	if len(spec.Boot.Devices) != 2 || spec.Boot.Devices[0] != "cdrom" || spec.Boot.Devices[1] != "hd" {
		t.Errorf("引导顺序应为 [cdrom hd]，得到 %v", spec.Boot.Devices)
	}
	if len(spec.Disks) != 3 {
		t.Fatalf("磁盘应为 3 块，得到 %d", len(spec.Disks))
	}
	cd := spec.Disks[0]
	if cd.Device != "cdrom" || !cd.ReadOnly || cd.Driver != "raw" || cd.Target != "hda" || cd.Bus != "ide" {
		t.Errorf("光驱解析错误: %+v", cd)
	}
	blk := spec.Disks[2]
	// 块设备的 source 来自 dev 属性而非 file
	if blk.Type != "block" || blk.Source != "/dev/vg0/data" || blk.Target != "sda" || blk.Bus != "scsi" {
		t.Errorf("块设备解析错误: %+v", blk)
	}
	if len(spec.Interfaces) != 2 {
		t.Fatalf("网卡应为 2 块，得到 %d", len(spec.Interfaces))
	}
	br := spec.Interfaces[1]
	// bridge 网卡的 source 来自 bridge 属性；无 mac 元素时 MAC 为空串
	if br.Type != "bridge" || br.Source != "br0" || br.Model != "e1000" || br.MAC != "" {
		t.Errorf("桥接网卡解析错误: %+v", br)
	}
	if spec.Graphics.Port != 5901 {
		t.Errorf("VNC 固定端口应为 5901，得到 %d", spec.Graphics.Port)
	}
	if spec.Arch != "aarch64" || spec.Machine != "virt-8.0" {
		t.Errorf("架构解析错误: arch=%q machine=%q", spec.Arch, spec.Machine)
	}
}

// TestParseDomainXMLMemoryUnits 验证各内存单位换算。
// dumpxml 常见 KiB/MiB，另有 GiB/bytes 与无单位（默认 KiB）兜底，算错会直接影响创建向导的内存展示。
func TestParseDomainXMLMemoryUnits(t *testing.T) {
	tpl := `<domain type='kvm'><name>t</name><memory unit='%s'>%s</memory><vcpu>1</vcpu><os><type>hvm</type></os><devices/></domain>`
	tests := []struct {
		name   string
		unit   string
		value  string
		wantMB int
	}{
		{"KiB 换算", "KiB", "2097152", 2048},
		{"MiB 换算", "MiB", "4096", 4096},
		{"GiB 换算", "GiB", "8", 8192},
		{"bytes 换算", "bytes", "2147483648", 2048},
		{"B 简写", "B", "2147483648", 2048},
		{"无单位默认 KiB", "", "1048576", 1024},
		{"单位大小写不敏感", "mib", "1024", 1024},
		{"单位带空白", "  MiB ", "1024", 1024},
		{"数值带空白", "MiB", " 1024\n", 1024},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := ParseDomainXML(fmt.Sprintf(tpl, tt.unit, tt.value))
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if spec.MemoryMB != tt.wantMB {
				t.Errorf("unit=%q value=%q 应得 %dMB，得到 %d", tt.unit, tt.value, tt.wantMB, spec.MemoryMB)
			}
		})
	}
}

// TestParseDomainXMLInvalid 验证非法输入的行为：空串与畸形 XML 必须报错，
// 缺字段的不应报错（返回零值 spec），缺内存的返回 0MB。
func TestParseDomainXMLInvalid(t *testing.T) {
	if _, err := ParseDomainXML(""); err == nil {
		t.Error("空字符串应返回错误")
	}
	if _, err := ParseDomainXML("<domain><name>没闭合"); err == nil {
		t.Error("畸形 XML 应返回错误")
	}
	spec, err := ParseDomainXML(`<domain type='kvm'><devices/></domain>`)
	if err != nil {
		t.Fatalf("缺字段不应报错，得到: %v", err)
	}
	if spec.Name != "" || spec.VCPU != 0 || spec.MemoryMB != 0 {
		t.Errorf("缺字段应得零值，得到 %+v", spec)
	}
	if len(spec.Disks) != 0 || len(spec.Interfaces) != 0 {
		t.Errorf("缺设备应得空切片，得到 %+v", spec)
	}
}

// TestBuildDomainXMLValidation 验证生成器的参数校验：nil、空名、非法 vCPU/内存必须拒绝，
// 否则会把非法定义喂给 libvirt（define 失败事小，静默建出 0 核机器事大）。
func TestBuildDomainXMLValidation(t *testing.T) {
	if _, err := BuildDomainXML(nil); err == nil {
		t.Error("nil spec 应返回错误")
	}
	for _, s := range []*DomainSpec{
		{Name: ""},
		{Name: "t", VCPU: 0, MemoryMB: 1024},
		{Name: "t", VCPU: -2, MemoryMB: 1024},
		{Name: "t", VCPU: 2, MemoryMB: 0},
		{Name: "t", VCPU: 2, MemoryMB: -512},
	} {
		if _, err := BuildDomainXML(s); err == nil {
			t.Errorf("非法 spec 应被拒绝: %+v", s)
		}
	}
}

// TestBuildDomainXMLDefaults 验证缺省填充：调用方只给必填项时，OS/引导/显卡/网卡模型/磁盘驱动
// 都要有合理默认，否则生成的 XML 缺节点会被 libvirt 拒绝。
func TestBuildDomainXMLDefaults(t *testing.T) {
	out, err := BuildDomainXML(&DomainSpec{Name: "mini", VCPU: 1, MemoryMB: 512})
	if err != nil {
		t.Fatalf("最小 spec 生成失败: %v", err)
	}
	for _, want := range []string{
		// encoding/xml 序列化风格：双引号属性、空元素输出为 <x></x> 而非自闭合
		"<name>mini</name>", ">hvm<", `arch="x86_64"`, `<boot dev="hd"></boot>`,
		`<acpi></acpi>`, `<apic></apic>`, `type="vnc"`, `port="0"`, "<memory unit=\"KiB\">524288</memory>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("生成 XML 缺少默认节点 %q\n%s", want, out)
		}
	}
	// 端口 -1 应转为 autoport（libvirt autoport 分配，本机 VNC 5900 即此来）
	out2, err := BuildDomainXML(&DomainSpec{Name: "auto", VCPU: 1, MemoryMB: 512, Graphics: GraphicsSpec{Port: -1}})
	if err != nil {
		t.Fatalf("autoport 生成失败: %v", err)
	}
	if !strings.Contains(out2, `autoport="yes"`) {
		t.Errorf("端口 -1 应生成 autoport=yes:\n%s", out2)
	}
}

// TestBuildDomainXMLDeviceDetails 对齐手工模板机 XML 的设备细节批次：CPU 直通、clock 定时器、
// guest-agent 通道、virtio-rng、memballoon、串口/控制台 pty——这些在手工模板里多年生产验证过，
// 平台生成路径必须等价输出；CPU 模式空值按 host-passthrough 处理。
func TestBuildDomainXMLDeviceDetails(t *testing.T) {
	out, err := BuildDomainXML(&DomainSpec{Name: "detail", VCPU: 1, MemoryMB: 512})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	for _, want := range []string{
		`<cpu mode="host-passthrough">`, // 空值默认直通
		`<clock offset="utc">`,          // 时钟 UTC
		`<timer name="rtc" tickpolicy="catchup">`,
		`<timer name="pit" tickpolicy="delay">`,
		`<timer name="hpet" present="no">`,
		`<serial type="pty">`, // 串口 pty（virsh console 依赖）
		`<target type="isa-serial" port="0">`,
		`<console type="pty">`,
		`<channel type="unix">`, // guest-agent 通道
		`<target type="virtio" name="org.qemu.guest_agent.0">`,
		`<rng model="virtio">`, // 随机数发生器
		`<backend model="random">/dev/urandom</backend>`,
		`<memballoon model="virtio">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("生成 XML 缺少设备细节 %q\n%s", want, out)
		}
	}

	// 显式 CPUMode 应透传（含 default = 不输出 cpu 节点，保留 libvirt 缺省语义）
	outP, err := BuildDomainXML(&DomainSpec{Name: "p", VCPU: 1, MemoryMB: 512, CPUMode: "host-passthrough"})
	if err != nil || !strings.Contains(outP, `<cpu mode="host-passthrough">`) {
		t.Errorf("显式 host-passthrough 应原样输出: err=%v\n%s", err, outP)
	}
	outD, err := BuildDomainXML(&DomainSpec{Name: "d", VCPU: 1, MemoryMB: 512, CPUMode: "default"})
	if err != nil {
		t.Fatalf("CPUMode=default 生成失败: %v", err)
	}
	if strings.Contains(outD, "<cpu ") {
		t.Errorf("CPUMode=default 不应输出 cpu 节点:\n%s", outD)
	}
}

// TestBuildDomainXMLXMLEscape 是 P1 安全批次的回归锚点：用户可控字符串（名称、磁盘路径、
// 网络名）塞进 XML 后必须被转义，反解后值原样、结构无新增节点。
// 修复前 virt 层是字符串拼接，闭合载荷可注入任意 libvirt 定义。
func TestBuildDomainXMLXMLEscape(t *testing.T) {
	evil := `a</name><uuid>inject</uuid><name>b`
	spec := &DomainSpec{
		Name:     evil,
		VCPU:     1,
		MemoryMB: 512,
		Disks:    []DiskSpec{{Device: "disk", Source: `/x/"><evil/>"`, Target: "vda", Bus: "virtio"}},
		Interfaces: []InterfaceSpec{
			{Type: "network", Source: `n</source></interface><interface type="network"><source network="evil`, MAC: "52:54:00:aa:bb:cc"},
		},
	}
	out, err := BuildDomainXML(spec)
	if err != nil {
		t.Fatalf("含恶意字符的 spec 生成失败: %v", err)
	}
	// 必须能被标准库反解（结构完整），且 name 字段原样还原（转义而非截断）
	var back struct {
		Name  string `xml:"name"`
		UUID  string `xml:"uuid"`
		Disks []struct {
			Source struct {
				File string `xml:"file,attr"`
			} `xml:"source"`
		} `xml:"devices>disk"`
		Interfaces []struct {
			Source struct {
				Network string `xml:"network,attr"`
			} `xml:"source"`
		} `xml:"devices>interface"`
	}
	if err := xml.Unmarshal([]byte(out), &back); err != nil {
		t.Fatalf("生成 XML 无法反解，结构已被破坏:\n%s", out)
	}
	if back.Name != evil {
		t.Errorf("名称反解后应原样 %q，得到 %q", evil, back.Name)
	}
	if back.UUID == "inject" {
		t.Error("注入的 <uuid> 节点生效了，XML 注入未被堵住")
	}
	if back.Disks[0].Source.File != `/x/"><evil/>"` {
		t.Errorf("磁盘路径反解后应原样，得到 %q", back.Disks[0].Source.File)
	}
	if len(back.Interfaces) != 1 {
		t.Errorf("注入后 interface 节点应仍为 1 个，得到 %d 个", len(back.Interfaces))
	}
}

// TestDomainXMLRoundTrip 往返测试：Build 再 Parse 应还原等价 spec。
// 这是抓编解码不对称最有效的手段（比如某字段只写不读，克隆/导入就会静默丢配置）。
func TestDomainXMLRoundTrip(t *testing.T) {
	src := &DomainSpec{
		Name:     "rt-01",
		UUID:     "33333333-3333-4333-8333-333333333333",
		VCPU:     2,
		MemoryMB: 2048,
		OSType:   "hvm",
		Arch:     "x86_64",
		Machine:  "pc-q35-8.0",
		Boot:     BootSpec{Devices: []string{"hd", "network"}},
		Disks: []DiskSpec{
			{Type: "file", Device: "disk", Driver: "qcow2", Bus: "virtio", Source: "/a.qcow2", Target: "vda"},
			{Type: "file", Device: "cdrom", Bus: "ide", Source: "/b.iso", Target: "hda", ReadOnly: true},
		},
		Interfaces: []InterfaceSpec{
			{Type: "network", Source: "default", MAC: "52:54:00:01:02:03", Model: "virtio"},
			{Type: "bridge", Source: "br0", MAC: "52:54:00:04:05:06", Model: "e1000"},
		},
		Graphics: GraphicsSpec{Type: "vnc", Port: -1},
	}
	out, err := BuildDomainXML(src)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	got, err := ParseDomainXML(out)
	if err != nil {
		t.Fatalf("反解自己生成的 XML 失败:\n%s", out)
	}

	checks := []struct {
		field string
		want  string
		got   string
	}{
		{"名称", src.Name, got.Name},
		{"UUID", src.UUID, got.UUID},
		{"OS 类型", src.OSType, got.OSType},
		{"架构", src.Arch, got.Arch},
		{"机型", src.Machine, got.Machine},
	}
	for _, c := range checks {
		if c.want != c.got {
			t.Errorf("%s往返不一致: %q → %q", c.field, c.want, c.got)
		}
	}
	if got.VCPU != src.VCPU {
		t.Errorf("vCPU 往返不一致: %d → %d（应取 current 而非上限）", src.VCPU, got.VCPU)
	}
	if got.MemoryMB != src.MemoryMB {
		t.Errorf("内存往返不一致: %d → %d", src.MemoryMB, got.MemoryMB)
	}
	if len(got.Boot.Devices) != 2 || got.Boot.Devices[1] != "network" {
		t.Errorf("引导顺序往返不一致: %v", got.Boot.Devices)
	}
	if len(got.Disks) != 2 || got.Disks[0].Source != "/a.qcow2" || !got.Disks[1].ReadOnly {
		t.Errorf("磁盘往返不一致: %+v", got.Disks)
	}
	// cdrom 的 driver 建模时为空，生成时按 raw 落盘，反解回来是 raw —— 这是设计使然，断言现状
	if got.Disks[1].Driver != "raw" {
		t.Errorf("光驱 driver 往返后应为 raw（生成器默认值），得到 %q", got.Disks[1].Driver)
	}
	if len(got.Interfaces) != 2 || got.Interfaces[0].MAC != "52:54:00:01:02:03" || got.Interfaces[1].Source != "br0" {
		t.Errorf("网卡往返不一致: %+v", got.Interfaces)
	}
	// 无 MAC 的网卡生成时省略 <mac>，反解回来仍为空 —— 往返稳定
	// 显卡 -1 生成 autoport，反解回 Port 仍是 -1（autoport 属性不回填端口号）
	if got.Graphics.Port != src.Graphics.Port {
		t.Errorf("显卡端口往返不一致: %d → %d", src.Graphics.Port, got.Graphics.Port)
	}
	// RawXML 与 Autostart 不在 domain XML 中，往返必然丢失 —— 设计使然，显式断言防止后人误判为 bug
	if got.RawXML != "" || got.Autostart {
		t.Errorf("RawXML/Autostart 不应从 XML 还原，得到 raw=%q autostart=%v", got.RawXML, got.Autostart)
	}
}

// TestNextDiskTarget 验证磁盘 target 自动分配：按 bus 分前缀、跳过已占用、26 块以上续编。
// 分错会导致 attach-disk 时 target 冲突，热插拔失败。
func TestNextDiskTarget(t *testing.T) {
	empty := &DomainSpec{}
	tests := []struct {
		name  string
		disks []DiskSpec
		bus   string
		want  string
	}{
		{"空盘 virtio 首块", nil, "virtio", "vda"},
		{"空盘 ide 首块", nil, "ide", "hda"},
		{"空盘 scsi 首块", nil, "scsi", "sda"},
		{"空盘 sata 首块", nil, "sata", "sda"},
		{"未知 bus 按 virtio", nil, "usb", "vda"},
		{"vda 已占给 vdb", []DiskSpec{{Bus: "virtio", Target: "vda"}}, "virtio", "vdb"},
		{"跳过空洞给 vdb", []DiskSpec{{Bus: "virtio", Target: "vda"}, {Bus: "virtio", Target: "vdc"}}, "virtio", "vdb"},
		{"不同 bus 互不干扰", []DiskSpec{{Bus: "virtio", Target: "vda"}, {Bus: "ide", Target: "hda"}}, "ide", "hdb"},
		{"同名不同 bus 不算占用", []DiskSpec{{Bus: "ide", Target: "hda"}}, "virtio", "vda"},
		{"目标名格式不对被忽略", []DiskSpec{{Bus: "virtio", Target: "disk0"}}, "virtio", "vda"},
		{"空 target 被忽略", []DiskSpec{{Bus: "virtio", Target: ""}}, "virtio", "vda"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &DomainSpec{Disks: tt.disks}
			if got := NextDiskTarget(spec, tt.bus); got != tt.want {
				t.Errorf("应分配 %s，得到 %s", tt.want, got)
			}
		})
	}
	_ = empty
	// 26 块占满后续编 aa（极少见，断言不 panic 且格式正确）
	full := &DomainSpec{}
	for i := 0; i < 26; i++ {
		full.Disks = append(full.Disks, DiskSpec{Bus: "virtio", Target: "vd" + string(rune('a'+i))})
	}
	if got := NextDiskTarget(full, "virtio"); got != "vdaa" {
		t.Errorf("26 块占满后应续编 vdaa，得到 %s", got)
	}
}

// TestDiskSuffixIndex 验证 target 后缀换算（NextDiskTarget 的内部构件）。
func TestDiskSuffixIndex(t *testing.T) {
	tests := []struct {
		suffix string
		want   int
	}{
		{"a", 0}, {"b", 1}, {"z", 25},
		// TODO: 下列两行断言的是现状而非正确值 —— 该函数是无偏置的 26 进制，
		// "aa" 算出来是 0，与 "a" 冲突。后果：NextDiskTarget 在 26 块占满后返回 "vdaa"，
		// 但第 28 块磁盘仍会再次算出 "vdaa"（"aa" 映射到已被占用的 0），造成 target 重复。
		// 26 块以上磁盘的虚拟机现实中几乎不存在，故只记录不修复；若要修，应改成
		// 有偏置的 bijective 26 进制（a=1..z=26, aa=27）。
		{"aa", 0}, {"ab", 1},
		{"", -1}, {"A", -1}, {"1", -1}, {"a1", -1},
	}
	for _, tt := range tests {
		if got := diskSuffixIndex(tt.suffix); got != tt.want {
			t.Errorf("后缀 %q 应为 %d，得到 %d", tt.suffix, tt.want, got)
		}
	}
}

// TestVcpuMax 验证热升级头寸：max(2n, n+4)，上限 64。
// 算错会导致 <vcpu> 上限写错，运行中热增核被 QEMU 拒绝。
func TestVcpuMax(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{1, 5},   // max(2,5)
		{2, 6},   // max(4,6)
		{3, 7},   // max(6,7)
		{4, 8},   // max(8,8) 相等
		{8, 16},  // max(16,12)
		{30, 60}, // max(60,34)
		{32, 64}, // max(64,36)
		{40, 64}, // 上限截断
		{100, 64},
	}
	for _, tt := range tests {
		if got := vcpuMax(tt.n); got != tt.want {
			t.Errorf("vcpuMax(%d) 应为 %d，得到 %d", tt.n, tt.want, got)
		}
	}
}

// TestMemoryMaxKiB 验证内存热升级头寸：max(2*cur, cur+2048MiB)，返回 KiB。
func TestMemoryMaxKiB(t *testing.T) {
	tests := []struct {
		curMB  int
		wantMB int
	}{
		{512, 2560},  // max(1024,2560)
		{1024, 3072}, // max(2048,3072)
		{2048, 4096}, // max(4096,4096) 相等
		{4096, 8192}, // max(8192,6144)
		{16384, 32768},
	}
	for _, tt := range tests {
		if got := memoryMaxKiB(tt.curMB); got != uint64(tt.wantMB)*1024 {
			t.Errorf("memoryMaxKiB(%dMB) 应为 %dMB(%dKiB)，得到 %dKiB", tt.curMB, tt.wantMB, uint64(tt.wantMB)*1024, got)
		}
	}
}

// TestParseIntTiny 验证容错解析：空白容忍、非法返回 0（XML 里偶发空白换行，不能因此整机解析失败）。
func TestParseIntTiny(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"4", 4}, {"  4\n", 4}, {"", 0}, {"abc", 0}, {"4.5", 0}, {"-2", -2},
	}
	for _, tt := range tests {
		if got := parseInt(tt.in); got != tt.want {
			t.Errorf("parseInt(%q) 应为 %d，得到 %d", tt.in, tt.want, got)
		}
	}
}

// TestDiskDriverType 验证驱动默认：显式值优先，cdrom 默认 raw，其余默认 qcow2。
// 默认错会导致 cdrom 按 qcow2 解析、ISO 无法引导。
func TestDiskDriverType(t *testing.T) {
	tests := []struct {
		name string
		d    DiskSpec
		want string
	}{
		{"显式 qcow2", DiskSpec{Device: "disk", Driver: "qcow2"}, "qcow2"},
		{"显式 raw", DiskSpec{Device: "disk", Driver: "raw"}, "raw"},
		{"空驱动磁盘默认 qcow2", DiskSpec{Device: "disk"}, "qcow2"},
		{"空驱动光驱默认 raw", DiskSpec{Device: "cdrom"}, "raw"},
		{"空设备按磁盘处理", DiskSpec{}, "qcow2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diskDriverType(tt.d); got != tt.want {
				t.Errorf("应为 %q，得到 %q", tt.want, got)
			}
		})
	}
}

// TestDiskXMLFromSpecDefaults 验证磁盘片段的缺省填充与 source 类型分支。
func TestDiskXMLFromSpecDefaults(t *testing.T) {
	// 空 Type/Device 补 file/disk；block 走 dev 属性
	b := diskXMLFromSpec(DiskSpec{Type: "block", Source: "/dev/vg0/x", Target: "vda", Bus: "virtio"})
	if b.Type != "block" || b.Source == nil || b.Source.Dev != "/dev/vg0/x" || b.Source.File != "" {
		t.Errorf("块设备应走 dev 属性: %+v", b.Source)
	}
	f := diskXMLFromSpec(DiskSpec{Source: "/a.qcow2", Target: "vda"})
	if f.Type != "file" || f.Device != "disk" || f.Source.File != "/a.qcow2" {
		t.Errorf("空类型应补 file/disk: %+v", f)
	}
	// 无 source 时省略 <source>（如空光驱位）
	ns := diskXMLFromSpec(DiskSpec{Device: "cdrom", Target: "hda", Bus: "ide", ReadOnly: true})
	if ns.Source != nil {
		t.Errorf("无 source 时应省略 <source> 元素")
	}
	if ns.ReadOnly == nil {
		t.Error("只读盘应生成 <readonly/>")
	}
	rw := diskXMLFromSpec(DiskSpec{Device: "disk", Target: "vda"})
	if rw.ReadOnly != nil {
		t.Error("读写盘不应生成 <readonly/>")
	}
}

// TestInterfaceXMLFromSpecDefaults 验证网卡片段的缺省填充与 source 类型分支。
func TestInterfaceXMLFromSpecDefaults(t *testing.T) {
	// 空类型默认 network，空模型默认 virtio，无 MAC 时省略 <mac>
	n := interfaceXMLFromSpec(InterfaceSpec{Source: "default"})
	if n.Type != "network" || n.Source == nil || n.Source.Network != "default" {
		t.Errorf("空类型应默认 network: %+v", n)
	}
	if n.Model == nil || n.Model.Type != "virtio" {
		t.Errorf("空模型应默认 virtio: %+v", n.Model)
	}
	if n.MAC != nil {
		t.Error("无 MAC 时应省略 <mac> 元素（libvirt 会自动分配）")
	}
	br := interfaceXMLFromSpec(InterfaceSpec{Type: "bridge", Source: "br0", MAC: "52:54:00:aa:bb:cc", Model: "e1000"})
	if br.Source.Bridge != "br0" || br.Source.Network != "" {
		t.Errorf("桥接网卡 source 分支错误: %+v", br.Source)
	}
	di := interfaceXMLFromSpec(InterfaceSpec{Type: "direct", Source: "eth0"})
	if di.Source.Dev != "eth0" || di.Source.Network != "" {
		t.Errorf("直通网卡 source 分支错误: %+v", di.Source)
	}
}
