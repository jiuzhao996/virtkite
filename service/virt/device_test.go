package virt

import (
	"encoding/xml"
	"testing"
)

// TestDomainDevicePresenceParse 验证标准设备存在性解析：channel/rng 挂在 <devices> 之下，
// 曾因少解析一层导致误判"不存在"重复 attach（libvirt 报 chardev 已存在）。
func TestDomainDevicePresenceParse(t *testing.T) {
	sample := `<?xml version="1.0"?><domain type="kvm"><name>t</name><devices>
<channel type="unix"><target type="virtio" name="org.qemu.guest_agent.0"/></channel>
<rng model="virtio"><backend model="random">/dev/urandom</backend></rng>
</devices></domain>`
	var p domainDevicePresence
	if err := xml.Unmarshal([]byte(sample), &p); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	hasAgent := false
	for _, ch := range p.Devices.Channels {
		if ch.Target.Name == "org.qemu.guest_agent.0" {
			hasAgent = true
		}
	}
	if !hasAgent {
		t.Error("应检测到 guest-agent 通道")
	}
	if p.Devices.Rng == nil {
		t.Error("应检测到 rng")
	}

	empty := `<domain><devices><disk><target dev="vda"/></disk></devices></domain>`
	var e domainDevicePresence
	if err := xml.Unmarshal([]byte(empty), &e); err != nil {
		t.Fatalf("解析空设备失败: %v", err)
	}
	if len(e.Devices.Channels) != 0 || e.Devices.Rng != nil {
		t.Error("无 channel/rng 时应判为不存在")
	}
}
