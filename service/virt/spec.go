package virt

import (
	"fmt"
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
	CPUMode    string          `json:"cpu_mode"` // CPU 模型：host-passthrough 直通宿主 CPU；空值在 BuildDomainXML 中也按直通处理
	Boot       BootSpec        `json:"boot"`
	Disks      []DiskSpec      `json:"disks"`
	Interfaces []InterfaceSpec `json:"interfaces"`
	Graphics   GraphicsSpec    `json:"graphics"`
	Autostart  bool            `json:"autostart"`
	CloudInit  *CloudInitSpec  `json:"cloud_init,omitempty"`
	RawXML     string          `json:"raw_xml,omitempty"` // dumpxml 原文（编辑回显）
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
