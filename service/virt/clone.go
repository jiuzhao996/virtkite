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

// CloneVolumeFromVol 基于父卷创建子卷（对应 virsh vol-clone；libvirt 自动写 qcow2 backing file）。
// 子卷存续期间父卷不可删除/移动。返回子卷路径。
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

	// newVolName 入参不含扩展名，函数内补 .qcow2
	childName := newVolName + ".qcow2"
	childXML := fmt.Sprintf(`<volume>
  <name>%s</name>
  <capacity unit="B">%d</capacity>
  <target>
    <format type='qcow2'/>
  </target>
</volume>`, childName, capacity)

	child, err := l.StorageVolCreateXMLFrom(pool, childXML, srcVol, 0)
	if err != nil {
		return "", fmt.Errorf("克隆卷失败（对应 virsh vol-clone）: %w", err)
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

// CloneVMFromSpec 基于源 DomainSpec 克隆整机（建卷 + 改 spec + define，不启动）。
// 对源首个 device=='disk' 的系统盘做 linked clone（CloneVolumeFromVol），
// 替换新 spec 的磁盘 source 为新卷路径，其余设备复制；UUID 重新生成。
// 返回新 domain 名（对应 virsh vol-clone + virsh define）。
//
// 依赖说明：本函数依赖 B1 产出的 service/virt/spec.go 中的 DomainSpec/DiskSpec 类型
// 与 BuildDomainXML 函数（契约锁定），当前 B1 尚未落盘，本函数暂无法编译；
// 待 spec.go 落地后即恢复正常。
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

	// 找首个 device=='disk' 的磁盘作为系统盘
	cloneIdx := -1
	for i := range source.Disks {
		if source.Disks[i].Device == "disk" {
			cloneIdx = i
			break
		}
	}
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

	// 复制 spec：改名称 / 重新生成 UUID / 替换系统盘 source
	spec := *source
	uuid, err := randomUUIDV4()
	if err != nil {
		return "", fmt.Errorf("生成新 UUID 失败: %w", err)
	}
	spec.Name = newName
	spec.UUID = uuid
	spec.Disks = make([]DiskSpec, len(source.Disks))
	copy(spec.Disks, source.Disks)
	spec.Disks[cloneIdx].Source = newPath
	spec.Disks[cloneIdx].BackingFile = srcPath // 记录父盘（展示用）
	spec.RawXML = ""                           // 克隆后 XML 由 BuildDomainXML 重建，raw_xml 不再适用

	xmlstr, err := BuildDomainXML(&spec)
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
