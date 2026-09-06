package virt

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"path"
	"strings"

	"github.com/digitalocean/go-libvirt"
)

// CloneVolumeFromVol 基于父卷创建**增量**子卷（linked clone，等价
// qemu-img create -f qcow2 -F qcow2 -b <父盘> <子盘>）。
// 子卷只保存写入差异，基础数据仍读父盘，因此建盘瞬时完成、几乎不占空间。
// 返回子卷路径。
//
// 实现要点（曾经的坑）：必须用 StorageVolCreateXML + XML 里声明 <backingStore>。
// 早期实现用的是 StorageVolCreateXMLFrom（等价 virsh vol-clone），
// 那个 API 做的是**全量数据拷贝**，产出的子卷没有 backing file
// ——即「增量克隆」名不副实。已实测确认：vol-clone 出来的卷
// qemu-img info 里没有 backing file 行，vol-dumpxml 里也没有 <backingStore>。
//
// 代价：子卷存续期间父卷不可删除/移动/改写。删除虚拟机时由
// ListBackingRefs 守卫拦住父盘（见 service/tasks 的 execDeleteVM）。
func (v *Virt) CloneVolumeFromVol(poolName, srcVolName, newVolName string) (string, error) {
	l, err := v.getConn()
	if err != nil {
		return "", err
	}

	pool, err := l.StoragePoolLookupByName(poolName)
	if err != nil {
		return "", fmt.Errorf("存储池 %s 不存在: %w", poolName, err)
	}
	srcVol, err := l.StorageVolLookupByName(pool, srcVolName)
	if err != nil {
		return "", fmt.Errorf("父卷 %s 不存在: %w", srcVolName, err)
	}
	// 取父卷虚拟容量（字节），子卷保持同容量
	_, capacity, _, err := l.StorageVolGetInfo(srcVol)
	if err != nil {
		return "", fmt.Errorf("获取父卷 %s 容量失败: %w", srcVolName, err)
	}
	// backingStore 需要父盘绝对路径
	srcPath, err := l.StorageVolGetPath(srcVol)
	if err != nil {
		return "", fmt.Errorf("获取父卷 %s 路径失败: %w", srcVolName, err)
	}

	// newVolName 入参不含扩展名，函数内补 .qcow2
	childName := newVolName + ".qcow2"
	// 卷 XML 走 encoding/xml 序列化（buildVolumeXMLWithBacking，storage.go），卷名自动转义无注入面
	childXML, err := buildVolumeXMLWithBacking(childName, volFormatQcow2, volUnitByte, int64(capacity), srcPath)
	if err != nil {
		return "", err
	}

	child, err := l.StorageVolCreateXML(pool, childXML, 0)
	if err != nil {
		return "", fmt.Errorf("创建增量克隆子卷失败（对应 virsh vol-create）: %w", err)
	}
	p, err := l.StorageVolGetPath(child)
	if err != nil {
		return "", fmt.Errorf("获取克隆卷路径失败: %w", err)
	}
	return p, nil
}

// LookupVolByPath 根据卷路径反查所属存储池名与卷名（对应 virsh vol-key + pool-name）。
// 导出供 handler 层在镜像/磁盘路径基础上做 linked clone 时反查父卷。
func (v *Virt) LookupVolByPath(volPath string) (poolName, volName string, err error) {
	return v.lookupVolPool(volPath)
}

// lookupVolPool 根据卷路径反查所属存储池名与卷名（对应 virsh vol-key + pool-name）。
// 镜像文件可能是直接落盘的（未走 StorageVolCreateXML），libvirt 卷缓存里查不到，
// 因此先按池路径前缀定位池并 refresh，再按文件名反查，保证直接落盘文件也可见。
func (v *Virt) lookupVolPool(volPath string) (poolName, volName string, err error) {
	l, err := v.getConn()
	if err != nil {
		return "", "", err
	}

	// 先直接尝试 libvirt 已知卷
	if vol, err := l.StorageVolLookupByPath(volPath); err == nil {
		pool, err := l.StoragePoolLookupByVolume(vol)
		if err != nil {
			return "", "", fmt.Errorf("查找卷 %s 所属存储池失败: %w", volPath, err)
		}
		name := vol.Name
		if name == "" {
			name = path.Base(volPath)
		}
		return pool.Name, name, nil
	}

	// 直接落盘文件：按池路径前缀定位并 refresh 后再查
	base := path.Base(volPath)
	pools, _, err := l.ConnectListAllStoragePools(1, libvirt.ConnectListStoragePoolsActive|libvirt.ConnectListStoragePoolsInactive)
	if err != nil {
		return "", "", fmt.Errorf("枚举存储池失败: %w", err)
	}
	for _, p := range pools {
		xmlstr, err := l.StoragePoolGetXMLDesc(p, 0)
		if err != nil {
			continue
		}
		var px struct {
			Target struct {
				Path string `xml:"path"`
			} `xml:"target"`
		}
		if err := xml.Unmarshal([]byte(xmlstr), &px); err != nil || px.Target.Path == "" {
			continue
		}
		if !strings.HasPrefix(volPath, px.Target.Path+"/") {
			continue
		}
		// 刷新池使落盘文件进入卷列表
		if active, _ := l.StoragePoolIsActive(p); active == 1 {
			_ = l.StoragePoolRefresh(p, 0)
		}
		if vol, err := l.StorageVolLookupByName(p, base); err == nil {
			_ = vol
			return p.Name, base, nil
		}
		return "", "", fmt.Errorf("池 %s 中未找到卷 %s（需先在存储管理中刷新）", p.Name, base)
	}
	return "", "", fmt.Errorf("未找到卷 %s 所属存储池", volPath)
}

// randomUUIDV4 生成一个符合 RFC 4122 的 v4 UUID 字符串。
// 参照 handler/vm.go randomUUID 思路，但 virt 层自实现小函数，避免跨层依赖。
func randomUUIDV4() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	), nil
}

// randomMACAddr 生成一个 KVM 保留前缀（52:54:00）的随机 MAC 地址。
// 前缀与 libvirt/QEMU 自动分配的保持一致，避免与物理网卡厂商 OUI 冲突。
// 克隆整机时必须为每块网卡重新生成：MAC 沿用源机会导致同网段地址冲突，
// 两台机器同时开机后 ARP 表错乱、网络双双不可用。
func randomMACAddr() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[0], b[1], b[2]), nil
}

// systemDiskIndex 返回源 spec 里首个 device=='disk' 的磁盘下标（系统盘），无则返回 -1。
// cdrom / floppy 不能作为 linked clone 的父盘。
func systemDiskIndex(source *DomainSpec) int {
	for i := range source.Disks {
		if source.Disks[i].Device == "disk" {
			return i
		}
	}
	return -1
}

// buildCloneSpec 由源 spec 派生克隆 spec（纯函数，不触碰 libvirt，便于单元测试）。
//
// 三件必须做对的事：
//  1. Disks 与 Interfaces 都要**深拷贝**。`*source` 只复制切片头，
//     直接改元素会同时污染源 spec（源 VM 的运行配置）；
//  2. UUID 重新生成；
//  3. **每块网卡的 MAC 都要重新生成**。libvirt 不会自动改 MAC——XML 里显式给了就照用，
//     沿用源机 MAC 会让克隆机与源机在同一网段地址冲突，两台同时开机后
//     ARP 表错乱、网络双双不可用。这是本函数存在的主要原因。
//
// diskIdx 为系统盘下标，newDiskPath 为已克隆好的子卷路径，srcDiskPath 为父盘路径（仅记录展示）。
func buildCloneSpec(source *DomainSpec, newName string, diskIdx int, newDiskPath, srcDiskPath string) (*DomainSpec, error) {
	uuid, err := randomUUIDV4()
	if err != nil {
		return nil, fmt.Errorf("生成新 UUID 失败: %w", err)
	}

	spec := *source
	spec.Name = newName
	spec.UUID = uuid

	spec.Disks = make([]DiskSpec, len(source.Disks))
	copy(spec.Disks, source.Disks)
	if diskIdx >= 0 && diskIdx < len(spec.Disks) {
		spec.Disks[diskIdx].Source = newDiskPath
		spec.Disks[diskIdx].BackingFile = srcDiskPath // 记录父盘（展示用）
	}

	spec.Interfaces = make([]InterfaceSpec, len(source.Interfaces))
	copy(spec.Interfaces, source.Interfaces)
	for i := range spec.Interfaces {
		mac, err := randomMACAddr()
		if err != nil {
			return nil, fmt.Errorf("生成克隆网卡 MAC 失败: %w", err)
		}
		spec.Interfaces[i].MAC = mac
	}

	spec.RawXML = "" // 克隆后 XML 由 BuildDomainXML 重建，raw_xml 不再适用
	return &spec, nil
}

// CloneVMFromSpec 基于源 DomainSpec 克隆整机（建卷 + 改 spec + define，不启动）。
// 对源首个 device=='disk' 的系统盘做 linked clone（CloneVolumeFromVol），
// 替换新 spec 的磁盘 source 为新卷路径，其余设备复制；
// UUID 与全部网卡 MAC 重新生成（二者都必须唯一，否则与源机冲突）。
// 返回新 domain 名（对应 qemu-img create -b + virsh define）。
func (v *Virt) CloneVMFromSpec(source *DomainSpec, newName string) (string, error) {
	if source == nil {
		return "", fmt.Errorf("源 DomainSpec 为空，无法克隆")
	}
	if source.Name == "" {
		return "", fmt.Errorf("源 DomainSpec 缺少名称，无法克隆")
	}
	if newName == "" {
		return "", fmt.Errorf("克隆虚拟机名称不能为空")
	}

	cloneIdx := systemDiskIndex(source)
	if cloneIdx < 0 {
		return "", fmt.Errorf("源虚拟机 %s 无系统盘，无法克隆", source.Name)
	}

	srcPath := source.Disks[cloneIdx].Source
	poolName, srcVolName, err := v.lookupVolPool(srcPath)
	if err != nil {
		return "", err
	}

	newVolName := newName + "-disk" + string(rune('a'+cloneIdx))
	newPath, err := v.CloneVolumeFromVol(poolName, srcVolName, newVolName)
	if err != nil {
		return "", err
	}

	spec, err := buildCloneSpec(source, newName, cloneIdx, newPath, srcPath)
	if err != nil {
		return "", err
	}

	xmlstr, err := BuildDomainXML(spec)
	if err != nil {
		return "", fmt.Errorf("生成克隆虚拟机 %s XML 失败: %w", newName, err)
	}

	l, err := v.getConn()
	if err != nil {
		return "", err
	}
	if _, err := l.DomainDefineXML(xmlstr); err != nil {
		return "", fmt.Errorf("定义克隆虚拟机 %s 失败（对应 virsh define）: %w", newName, err)
	}
	return newName, nil
}
