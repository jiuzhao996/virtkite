package virt

import (
	"regexp"
	"strings"
	"testing"
)

// macFormat KVM 保留前缀 + 三段随机字节的小写十六进制。
var macFormat = regexp.MustCompile(`^52:54:00:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}$`)

// uuidV4Format RFC 4122 v4：第三段以 4 开头，第四段首字符落在 8/9/a/b。
var uuidV4Format = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// newSourceSpec 构造一台带两块盘、两块网卡的源虚拟机配置，用于克隆测试。
func newSourceSpec() *DomainSpec {
	return &DomainSpec{
		Name:     "web-base",
		UUID:     "11111111-1111-4111-8111-111111111111",
		VCPU:     2,
		MemoryMB: 2048,
		Disks: []DiskSpec{
			// 首位放 cdrom，验证系统盘定位不会误取光驱
			{Type: "file", Device: "cdrom", Driver: "raw", Bus: "ide", Source: "/iso/ubuntu.iso", Target: "hda", ReadOnly: true},
			{Type: "file", Device: "disk", Driver: "qcow2", Bus: "virtio", Source: "/var/lib/libvirt/images/web-base.qcow2", Target: "vda"},
			{Type: "file", Device: "disk", Driver: "qcow2", Bus: "virtio", Source: "/var/lib/libvirt/images/web-base-d1.qcow2", Target: "vdb"},
		},
		Interfaces: []InterfaceSpec{
			{Type: "network", Source: "default", MAC: "52:54:00:aa:bb:cc", Model: "virtio"},
			{Type: "network", Source: "vmops-net", MAC: "52:54:00:dd:ee:ff", Model: "virtio"},
		},
		RawXML: "<domain>源机原始 XML</domain>",
	}
}

// TestRandomMACAddrFormatAndUniqueness 验证 MAC 生成器的格式与碰撞概率。
func TestRandomMACAddrFormatAndUniqueness(t *testing.T) {
	seen := make(map[string]bool, 500)
	for i := 0; i < 500; i++ {
		mac, err := randomMACAddr()
		if err != nil {
			t.Fatalf("生成 MAC 失败: %v", err)
		}
		if !macFormat.MatchString(mac) {
			t.Fatalf("MAC 格式不符（应为 52:54:00:xx:xx:xx）: %q", mac)
		}
		if seen[mac] {
			t.Fatalf("500 次生成出现重复 MAC: %s", mac)
		}
		seen[mac] = true
	}
}

// TestRandomUUIDV4Format 验证 UUID 生成器符合 RFC 4122 v4。
func TestRandomUUIDV4Format(t *testing.T) {
	for i := 0; i < 100; i++ {
		uuid, err := randomUUIDV4()
		if err != nil {
			t.Fatalf("生成 UUID 失败: %v", err)
		}
		if !uuidV4Format.MatchString(uuid) {
			t.Fatalf("UUID 格式不符 RFC 4122 v4: %q", uuid)
		}
	}
}

// TestSystemDiskIndexSkipsCDROM 验证系统盘定位跳过 cdrom（cdrom 不能作 linked clone 父盘）。
func TestSystemDiskIndexSkipsCDROM(t *testing.T) {
	if got := systemDiskIndex(newSourceSpec()); got != 1 {
		t.Errorf("系统盘下标应为 1（跳过下标 0 的 cdrom），得到 %d", got)
	}
	onlyCDROM := &DomainSpec{Disks: []DiskSpec{{Device: "cdrom"}}}
	if got := systemDiskIndex(onlyCDROM); got != -1 {
		t.Errorf("只有 cdrom 时应返回 -1，得到 %d", got)
	}
	if got := systemDiskIndex(&DomainSpec{}); got != -1 {
		t.Errorf("无磁盘时应返回 -1，得到 %d", got)
	}
}

// TestBuildCloneSpecRegeneratesAllMACs 是本文件的核心用例：
// 克隆机的每块网卡 MAC 都必须与源机不同。
// 修复前 `spec := *source` 只复制切片头、Interfaces 未深拷贝也未换 MAC，
// 克隆机与源机 MAC 完全相同，两台同时开机即同网段地址冲突。
func TestBuildCloneSpecRegeneratesAllMACs(t *testing.T) {
	source := newSourceSpec()
	srcMACs := []string{source.Interfaces[0].MAC, source.Interfaces[1].MAC}

	clone, err := buildCloneSpec(source, "web-clone", 1,
		"/var/lib/libvirt/images/web-clone-diskb.qcow2",
		"/var/lib/libvirt/images/web-base.qcow2")
	if err != nil {
		t.Fatalf("派生克隆 spec 失败: %v", err)
	}

	if len(clone.Interfaces) != len(source.Interfaces) {
		t.Fatalf("网卡数量应保持 %d，得到 %d", len(source.Interfaces), len(clone.Interfaces))
	}

	cloneMACs := map[string]bool{}
	for i, nic := range clone.Interfaces {
		if !macFormat.MatchString(nic.MAC) {
			t.Errorf("网卡 %d 的 MAC 格式不符: %q", i, nic.MAC)
		}
		if nic.MAC == srcMACs[i] {
			t.Errorf("网卡 %d 的 MAC 与源机相同（%s），会造成同网段地址冲突", i, nic.MAC)
		}
		if cloneMACs[nic.MAC] {
			t.Errorf("克隆机内部两块网卡 MAC 重复: %s", nic.MAC)
		}
		cloneMACs[nic.MAC] = true

		// MAC 之外的网卡属性必须原样保留
		if nic.Type != source.Interfaces[i].Type || nic.Source != source.Interfaces[i].Source || nic.Model != source.Interfaces[i].Model {
			t.Errorf("网卡 %d 的非 MAC 属性被改动: %+v", i, nic)
		}
	}

	// 源 spec 不能被污染（深拷贝的意义）
	for i, want := range srcMACs {
		if source.Interfaces[i].MAC != want {
			t.Errorf("源机网卡 %d 的 MAC 被改动: %s → %s", i, want, source.Interfaces[i].MAC)
		}
	}
}

// TestBuildCloneSpecDeepCopiesDisks 验证磁盘切片深拷贝且只改系统盘的 source。
func TestBuildCloneSpecDeepCopiesDisks(t *testing.T) {
	source := newSourceSpec()
	const newPath = "/var/lib/libvirt/images/web-clone-diskb.qcow2"
	const srcPath = "/var/lib/libvirt/images/web-base.qcow2"

	clone, err := buildCloneSpec(source, "web-clone", 1, newPath, srcPath)
	if err != nil {
		t.Fatalf("派生克隆 spec 失败: %v", err)
	}

	if clone.Disks[1].Source != newPath {
		t.Errorf("系统盘 source 应替换为 %s，得到 %s", newPath, clone.Disks[1].Source)
	}
	if clone.Disks[1].BackingFile != srcPath {
		t.Errorf("系统盘 backing file 应记录父盘 %s，得到 %s", srcPath, clone.Disks[1].BackingFile)
	}
	// 源机系统盘不受影响
	if source.Disks[1].Source != srcPath {
		t.Errorf("源机系统盘 source 被改动: %s", source.Disks[1].Source)
	}
	if source.Disks[1].BackingFile != "" {
		t.Errorf("源机系统盘 backing file 被写入: %s", source.Disks[1].BackingFile)
	}
	// cdrom 与数据盘原样保留
	if clone.Disks[0].Source != "/iso/ubuntu.iso" || clone.Disks[0].Device != "cdrom" {
		t.Errorf("cdrom 不应被改动: %+v", clone.Disks[0])
	}
	if clone.Disks[2].Source != source.Disks[2].Source {
		t.Errorf("数据盘 source 不应被改动: %s", clone.Disks[2].Source)
	}
}

// TestBuildCloneSpecIdentityFields 验证名称、UUID、RawXML 的处理。
func TestBuildCloneSpecIdentityFields(t *testing.T) {
	source := newSourceSpec()
	clone, err := buildCloneSpec(source, "web-clone", 1, "/new.qcow2", "/old.qcow2")
	if err != nil {
		t.Fatalf("派生克隆 spec 失败: %v", err)
	}

	if clone.Name != "web-clone" {
		t.Errorf("克隆机名称应为 web-clone，得到 %s", clone.Name)
	}
	if clone.UUID == source.UUID {
		t.Error("克隆机 UUID 与源机相同，libvirt 会拒绝定义")
	}
	if !uuidV4Format.MatchString(clone.UUID) {
		t.Errorf("克隆机 UUID 格式不符 RFC 4122 v4: %q", clone.UUID)
	}
	if clone.RawXML != "" {
		t.Errorf("RawXML 应清空（克隆后 XML 由 BuildDomainXML 重建），得到 %q", clone.RawXML)
	}
	// 硬件规格沿用源机
	if clone.VCPU != source.VCPU || clone.MemoryMB != source.MemoryMB {
		t.Errorf("硬件规格应沿用源机，得到 vcpu=%d mem=%d", clone.VCPU, clone.MemoryMB)
	}
	// 源机身份字段不受影响
	if source.Name != "web-base" || source.RawXML == "" {
		t.Errorf("源机身份字段被污染: name=%s rawXML空=%v", source.Name, source.RawXML == "")
	}
}

// TestBuildCloneSpecNoInterfaces 验证无网卡的虚拟机（纯串口机）也能克隆。
func TestBuildCloneSpecNoInterfaces(t *testing.T) {
	source := &DomainSpec{
		Name:  "headless",
		Disks: []DiskSpec{{Device: "disk", Source: "/a.qcow2", Target: "vda"}},
	}
	clone, err := buildCloneSpec(source, "headless-clone", 0, "/b.qcow2", "/a.qcow2")
	if err != nil {
		t.Fatalf("无网卡时不应报错: %v", err)
	}
	if len(clone.Interfaces) != 0 {
		t.Errorf("无网卡时克隆机也应无网卡，得到 %d 块", len(clone.Interfaces))
	}
}

// TestCloneSpecProducesValidXML 端到端验证：派生的 spec 能生成含新 MAC 的合法 XML，
// 且不含源机 MAC（BuildDomainXML 是真正写给 libvirt 的那一步）。
func TestCloneSpecProducesValidXML(t *testing.T) {
	source := newSourceSpec()
	clone, err := buildCloneSpec(source, "web-clone", 1, "/new.qcow2", "/old.qcow2")
	if err != nil {
		t.Fatalf("派生克隆 spec 失败: %v", err)
	}

	xmlStr, err := BuildDomainXML(clone)
	if err != nil {
		t.Fatalf("生成克隆 XML 失败: %v", err)
	}
	for _, srcMAC := range []string{"52:54:00:aa:bb:cc", "52:54:00:dd:ee:ff"} {
		if strings.Contains(xmlStr, srcMAC) {
			t.Errorf("克隆机 XML 仍含源机 MAC %s", srcMAC)
		}
	}
	for _, nic := range clone.Interfaces {
		if !strings.Contains(xmlStr, nic.MAC) {
			t.Errorf("克隆机 XML 缺少新 MAC %s", nic.MAC)
		}
	}
	if !strings.Contains(xmlStr, "<name>web-clone</name>") {
		t.Error("克隆机 XML 缺少新名称")
	}
}
