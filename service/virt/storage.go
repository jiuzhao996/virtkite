package virt

import (
	"encoding/xml"
	"fmt"

	"github.com/digitalocean/go-libvirt"
)

// xmlDecode 使用标准库 encoding/xml 解析存储池 XML。
func xmlDecode(data string, v interface{}) error {
	return xml.Unmarshal([]byte(data), v)
}

// PoolInfo 存储池详情（供前端展示）。
type PoolInfo struct {
	Name       string    `json:"name"`
	Active     bool      `json:"active"`
	Persistent bool      `json:"persistent"`
	Path       string    `json:"path"`
	Capacity   uint64    `json:"capacity"`
	Allocation uint64    `json:"allocation"`
	Available  uint64    `json:"available"`
	VolCount   int       `json:"vol_count"`
	Volumes    []VolInfo `json:"volumes,omitempty"`
}

// VolInfo 存储卷信息。
type VolInfo struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Capacity   uint64 `json:"capacity"`
	Allocation uint64 `json:"allocation"`
}

// CreateVolume 在指定存储池中创建 qcow2 格式存储卷（对应 virsh vol-create-as）。
// poolName 指定存储池名（如 default / vmops）；diskName 为卷文件名（不含扩展名，函数会自动加 .qcow2）。
// capacityGB 为卷容量（GB）。
func (v *Virt) CreateVolume(poolName, diskName string, capacityGB int) (string, error) {
	l, err := v.getConn()
	if err != nil {
		return "", err
	}

	pool, err := l.StoragePoolLookupByName(poolName)
	if err != nil {
		return "", fmt.Errorf("存储池 %s 不存在: %v", poolName, err)
	}

	volName := diskName + ".qcow2"
	xml := fmt.Sprintf(`<volume>
  <name>%s</name>
  <capacity unit="G">%d</capacity>
  <target>
    <format type="qcow2"/>
  </target>
</volume>`, volName, capacityGB)

	if _, err := l.StorageVolCreateXML(pool, xml, 0); err != nil {
		return "", fmt.Errorf("创建存储卷失败: %w", err)
	}

	return volName, nil
}

// DeleteVolume 删除存储池中指定名称的存储卷（对应 virsh vol-delete）。
func (v *Virt) DeleteVolume(poolName, diskName string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}

	pool, err := l.StoragePoolLookupByName(poolName)
	if err != nil {
		return fmt.Errorf("存储池 %s 不存在: %v", poolName, err)
	}

	vol, err := l.StorageVolLookupByName(pool, diskName)
	if err != nil {
		// 卷不存在视为已删除，不报错
		return nil
	}

	if err := l.StorageVolDelete(vol, libvirt.StorageVolDeleteNormal); err != nil {
		return fmt.Errorf("删除存储卷失败: %w", err)
	}
	return nil
}

// GetPoolPath 返回存储池的目标路径（解析 pool XML 的 <target><path>）。
func (v *Virt) GetPoolPath(poolName string) (string, error) {
	l, err := v.getConn()
	if err != nil {
		return "", err
	}

	pool, err := l.StoragePoolLookupByName(poolName)
	if err != nil {
		return "", fmt.Errorf("存储池 %s 不存在: %v", poolName, err)
	}

	xml, err := l.StoragePoolGetXMLDesc(pool, 0)
	if err != nil {
		return "", fmt.Errorf("获取存储池配置失败: %w", err)
	}

	// 解析 <target><path>xxx</path></target>
	var p struct {
		Target struct {
			Path string `xml:"path"`
		} `xml:"target"`
	}
	if err := xmlDecode(xml, &p); err != nil {
		return "", fmt.Errorf("解析存储池路径失败: %w", err)
	}
	if p.Target.Path == "" {
		return "", fmt.Errorf("存储池 %s 无 target path", poolName)
	}
	return p.Target.Path, nil
}

// ListPools 返回所有存储池名称列表（对应 virsh pool-list --all）。
func (v *Virt) ListPools() ([]string, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}

	flags := libvirt.ConnectListStoragePoolsActive | libvirt.ConnectListStoragePoolsInactive
	pools, _, err := l.ConnectListAllStoragePools(1, flags)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(pools))
	for _, p := range pools {
		names = append(names, p.Name)
	}
	return names, nil
}

// ListPoolInfos 返回所有存储池详情（含容量/状态/路径，供前端展示）。
func (v *Virt) ListPoolInfos() ([]PoolInfo, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}

	flags := libvirt.ConnectListStoragePoolsActive | libvirt.ConnectListStoragePoolsInactive
	pools, _, err := l.ConnectListAllStoragePools(1, flags)
	if err != nil {
		return nil, err
	}

	infos := make([]PoolInfo, 0, len(pools))
	for _, p := range pools {
		info, err := v.getPoolInfo(l, p)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// GetPoolInfo 返回指定存储池详情。
func (v *Virt) GetPoolInfo(poolName string) (*PoolInfo, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}
	pool, err := l.StoragePoolLookupByName(poolName)
	if err != nil {
		return nil, fmt.Errorf("存储池 %s 不存在: %v", poolName, err)
	}
	info, err := v.getPoolInfo(l, pool)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// getPoolInfo 聚合单池详情。
func (v *Virt) getPoolInfo(l *libvirt.Libvirt, pool libvirt.StoragePool) (PoolInfo, error) {
	info := PoolInfo{Name: pool.Name}

	active, err := l.StoragePoolIsActive(pool)
	if err == nil {
		info.Active = active == 1
	}
	persistent, err := l.StoragePoolIsPersistent(pool)
	if err == nil {
		info.Persistent = persistent == 1
	}

	state, capacity, allocation, available, err := l.StoragePoolGetInfo(pool)
	if err == nil {
		_ = state
		info.Capacity = capacity
		info.Allocation = allocation
		info.Available = available
	}

	if poolXML, err := l.StoragePoolGetXMLDesc(pool, 0); err == nil {
		var p struct {
			Target struct {
				Path string `xml:"path"`
			} `xml:"target"`
		}
		if err := xmlDecode(poolXML, &p); err == nil {
			info.Path = p.Target.Path
		}
	}

	// 卷列表
	names, err := l.StoragePoolListVolumes(pool, 0)
	if err == nil {
		info.VolCount = len(names)
		info.Volumes = make([]VolInfo, 0, len(names))
		for _, n := range names {
			vol, err := l.StorageVolLookupByName(pool, n)
			if err != nil {
				continue
			}
			vi := VolInfo{Name: n}
			if path, err := l.StorageVolGetPath(vol); err == nil {
				vi.Path = path
			}
			if t, capa, alloc, err := l.StorageVolGetInfo(vol); err == nil {
				_ = t
				vi.Capacity = capa
				vi.Allocation = alloc
			}
			info.Volumes = append(info.Volumes, vi)
		}
	}

	return info, nil
}

// CreateDirPool 创建并启动一个目录类型存储池（对应 virsh pool-define-as + pool-start + pool-autostart）。
func (v *Virt) CreateDirPool(name, path string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}

	xml := fmt.Sprintf(`<pool type='dir'>
  <name>%s</name>
  <target>
    <path>%s</path>
  </target>
</pool>`, name, path)

	pool, err := l.StoragePoolDefineXML(xml, 0)
	if err != nil {
		return fmt.Errorf("定义存储池失败: %w", err)
	}
	if err := l.StoragePoolCreate(pool, 0); err != nil {
		return fmt.Errorf("启动存储池失败: %w", err)
	}
	if err := l.StoragePoolSetAutostart(pool, 1); err != nil {
		return fmt.Errorf("设置自动启动失败: %w", err)
	}
	return nil
}

// DeleteDirPool 删除存储池（停止 + 删除定义）。
func (v *Virt) DeleteDirPool(name string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}

	pool, err := l.StoragePoolLookupByName(name)
	if err != nil {
		return fmt.Errorf("存储池 %s 不存在: %v", name, err)
	}

	if err := l.StoragePoolDestroy(pool); err != nil {
		return fmt.Errorf("停止存储池失败: %w", err)
	}
	if err := l.StoragePoolUndefine(pool); err != nil {
		return fmt.Errorf("删除存储池定义失败: %w", err)
	}
	return nil
}

// CreateVolumeCustom 在指定池创建自定义卷（支持 qcow2/raw 与任意大小）。
func (v *Virt) CreateVolumeCustom(poolName, volName, format string, capacityGB int) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	pool, err := l.StoragePoolLookupByName(poolName)
	if err != nil {
		return fmt.Errorf("存储池 %s 不存在: %v", poolName, err)
	}

	if format == "" {
		format = "qcow2"
	}
	xml := fmt.Sprintf(`<volume>
  <name>%s</name>
  <capacity unit="G">%d</capacity>
  <target>
    <format type="%s"/>
  </target>
</volume>`, volName, capacityGB, format)

	if _, err := l.StorageVolCreateXML(pool, xml, 0); err != nil {
		return fmt.Errorf("创建存储卷失败: %w", err)
	}
	return nil
}
