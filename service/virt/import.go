package virt

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/digitalocean/go-libvirt"
)

// DomainDetail 宿主机上已定义域的摘要信息（存量 VM 纳管扫描/导入用）。
type DomainDetail struct {
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
	State    string `json:"state"`
	VCPU     int    `json:"vcpu"`
	MemoryMB int    `json:"memory_mb"`
	DiskPath string `json:"disk_path"`
	DiskGB   int    `json:"disk_gb"`
	MAC      string `json:"mac_address"`
	OSType   string `json:"os_type"`
	Managed  bool   `json:"managed"`
}

// domainXML 用于解析 domain dumpxml 的关键字段（内存/vCPU/磁盘/MAC）。
type domainXML struct {
	UUID   string `xml:"uuid"`
	Memory struct {
		Value uint64 `xml:",chardata"`
		Unit  string `xml:"unit,attr"`
	} `xml:"memory"`
	VCPU struct {
		Value uint64 `xml:",chardata"`
	} `xml:"vcpu"`
	OS struct {
		Type struct {
			Arch string `xml:"arch,attr"`
		} `xml:"type"`
	} `xml:"os"`
	Devices struct {
		Disks []struct {
			Device string `xml:"device,attr"`
			Source struct {
				File string `xml:"file,attr"`
			} `xml:"source"`
		} `xml:"disk"`
		Interfaces []struct {
			MAC struct {
				Address string `xml:"address,attr"`
			} `xml:"mac"`
		} `xml:"interface"`
	} `xml:"devices"`
}

// ListDomainsWithDetail 枚举宿主机上所有已定义域（对应 virsh list --all），并解析硬件摘要。
// 遍历顺序即 libvirt 返回顺序；单个域解析失败会被跳过。
func (v *Virt) ListDomainsWithDetail() ([]DomainDetail, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}

	flags := libvirt.ConnectListDomainsActive | libvirt.ConnectListDomainsInactive
	domains, _, err := l.ConnectListAllDomains(1, flags)
	if err != nil {
		return nil, fmt.Errorf("枚举域失败: %v", err)
	}

	details := make([]DomainDetail, 0, len(domains))
	for _, d := range domains {
		dd, err := v.domainDetail(l, d)
		if err != nil {
			continue
		}
		details = append(details, dd)
	}
	return details, nil
}

// domainDetail 抓取单个域的实时状态与硬件/磁盘/MAC 摘要。
func (v *Virt) domainDetail(l *libvirt.Libvirt, dom libvirt.Domain) (DomainDetail, error) {
	var dd DomainDetail
	dd.Name = dom.Name

	state, _, err := l.DomainGetState(dom, 0)
	if err == nil {
		dd.State = StateToPlatform(state)
	}

	xmlstr, err := l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return dd, fmt.Errorf("域 %s 获取 XML 失败: %v", dom.Name, err)
	}

	var dx domainXML
	if err := xml.Unmarshal([]byte(xmlstr), &dx); err != nil {
		return dd, fmt.Errorf("域 %s XML 解析失败: %v", dom.Name, err)
	}
	dd.UUID = dx.UUID
	dd.VCPU = int(dx.VCPU.Value)
	dd.MemoryMB = memoryToMiB(dx.Memory.Value, dx.Memory.Unit)
	dd.OSType = dx.OS.Type.Arch
	for _, iface := range dx.Devices.Interfaces {
		if iface.MAC.Address != "" {
			dd.MAC = iface.MAC.Address
			break
		}
	}
	for _, disk := range dx.Devices.Disks {
		if disk.Device == "disk" && disk.Source.File != "" {
			dd.DiskPath = disk.Source.File
			break
		}
	}
	return dd, nil
}

// memoryToMiB 将 libvirt 内存数值换算为 MiB（缺省/未知单位按 KiB 处理）。
func memoryToMiB(v uint64, unit string) int {
	switch strings.ToLower(unit) {
	case "kib", "kb", "":
		return int(v / 1024)
	case "mib", "mb":
		return int(v)
	case "gib", "gb":
		return int(v * 1024)
	default:
		return int(v / 1024)
	}
}

// DiskSizeGB 返回磁盘文件容量（GiB，向上取整）。路径不属于任何池或不存在时返回 0（不视为错误）。
func (v *Virt) DiskSizeGB(path string) int {
	l, err := v.getConn()
	if err != nil {
		return 0
	}
	vol, err := l.StorageVolLookupByPath(path)
	if err != nil {
		return 0
	}
	_, capacity, _, err := l.StorageVolGetInfo(vol)
	if err != nil || capacity == 0 {
		return 0
	}
	gb := capacity / (1024 * 1024 * 1024)
	if capacity%(1024*1024*1024) != 0 {
		gb++
	}
	return int(gb)
}
