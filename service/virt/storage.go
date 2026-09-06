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

// 以下为存储池/存储卷 XML 生成结构（按 AGENTS.md「XML 用标准库 encoding/xml」，
// 参照 spec.go 的 BuildDomainXML 写法）。用 xml.Marshal 而非字符串拼接：
// 池名/路径/卷名/格式等外部输入中的 XML 元字符由标准库自动转义，
// 无法闭合标签注入 <target><path>/</path></target> 之类额外节点。

// volCapacityXML <capacity unit="G">20</capacity>
type volCapacityXML struct {
	Unit  string `xml:"unit,attr"`
	Value int64  `xml:",chardata"`
}

// volFormatXML <format type="qcow2"/>
type volFormatXML struct {
	Type string `xml:"type,attr"`
}

// volTargetXML <target><format type="qcow2"/></target>
type volTargetXML struct {
	Format volFormatXML `xml:"format"`
}

// volumeXML 存储卷定义根元素（对应 virsh vol-create 的输入 XML）。
type volumeXML struct {
	XMLName      xml.Name       `xml:"volume"`
	Name         string         `xml:"name"`
	Capacity     volCapacityXML `xml:"capacity"`
	Target       volTargetXML   `xml:"target"`
	BackingStore *volBackingXML `xml:"backingStore,omitempty"` // 仅增量克隆子卷填充
}

// volBackingXML <backingStore>：声明 qcow2 后备文件，使新卷成为父卷的增量子卷。
// 这是实现 linked clone 的关键——等价 qemu-img create -b <父盘> -F qcow2。
type volBackingXML struct {
	Path   string       `xml:"path"`
	Format volFormatXML `xml:"format"`
}

// poolTargetXML <target><path>/data/pool</path></target>
type poolTargetXML struct {
	Path string `xml:"path"`
}

// poolXML 存储池定义根元素（对应 virsh pool-define 的输入 XML）。
type poolXML struct {
	XMLName xml.Name      `xml:"pool"`
	Type    string        `xml:"type,attr"`
	Name    string        `xml:"name"`
	Target  poolTargetXML `xml:"target"`
}

// 存储 XML 固定取值（命名常量，避免散落字面量）。
const (
	volUnitGiB     = "G"     // <capacity unit="G">：按 GB 声明容量
	volUnitByte    = "B"     // <capacity unit="B">：按字节声明容量（克隆时与父卷等容量）
	volFormatQcow2 = "qcow2" // <format type>：默认卷格式
	poolTypeDir    = "dir"   // <pool type>：目录型存储池
)

// buildVolumeXML 生成存储卷定义 XML（对应 virsh vol-create 的输入文件）。
// name/format 为外部可控输入，经 encoding/xml 序列化自动转义，无 XML 注入面。
func buildVolumeXML(name, format, unit string, capacity int64) (string, error) {
	return buildVolumeXMLWithBacking(name, format, unit, capacity, "")
}

// buildVolumeXMLWithBacking 生成存储卷定义 XML；backingPath 非空时附带 <backingStore>，
// 新卷即成为该父盘的 qcow2 增量子卷（linked clone），只存写入差异。
// 父盘格式固定按 qcow2 声明：本项目所有池卷均为 qcow2。
func buildVolumeXMLWithBacking(name, format, unit string, capacity int64, backingPath string) (string, error) {
	v := volumeXML{
		Name:     name,
		Capacity: volCapacityXML{Unit: unit, Value: capacity},
		Target:   volTargetXML{Format: volFormatXML{Type: format}},
	}
	if backingPath != "" {
		v.BackingStore = &volBackingXML{
			Path:   backingPath,
			Format: volFormatXML{Type: volFormatQcow2},
		}
	}
	out, err := xml.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("生成存储卷 XML 失败: %w", err)
	}
	return string(out), nil
}

// buildDirPoolXML 生成目录型存储池定义 XML（对应 virsh pool-define 的输入文件）。
// name/path 为外部可控输入，经 encoding/xml 序列化自动转义，无 XML 注入面。
func buildDirPoolXML(name, path string) (string, error) {
	p := poolXML{
		Type:   poolTypeDir,
		Name:   name,
		Target: poolTargetXML{Path: path},
	}
	out, err := xml.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("生成存储池 XML 失败: %w", err)
	}
	return string(out), nil
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
		return "", fmt.Errorf("存储池 %s 不存在: %w", poolName, err)
	}

	volName := diskName + ".qcow2"
	xmlstr, err := buildVolumeXML(volName, volFormatQcow2, volUnitGiB, int64(capacityGB))
	if err != nil {
		return "", err
	}

	if _, err := l.StorageVolCreateXML(pool, xmlstr, 0); err != nil {
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
		return fmt.Errorf("存储池 %s 不存在: %w", poolName, err)
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

// ListBackingRefs 枚举存储池内所有卷的 backing file 引用，
// 返回「父卷路径 → 依赖它的子卷名列表」（对应 virsh vol-dumpxml 的 <backingStore><path>）。
//
// 用途：删除卷之前必须确认没有子卷把它当作 qcow2 backing file。
// 本项目的增量克隆（linked clone）子盘只存增量、基础数据仍读父盘，
// 父盘一旦被删，backing chain 断裂，所有子虚拟机的磁盘立刻不可读且无法恢复。
// 池内没有任何 backing 引用时返回空 map（非 nil），调用方可直接查。
func (v *Virt) ListBackingRefs(poolName string) (map[string][]string, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}

	pool, err := l.StoragePoolLookupByName(poolName)
	if err != nil {
		return nil, fmt.Errorf("存储池 %s 不存在: %w", poolName, err)
	}
	// 先刷新池，保证直接落盘的文件也进入卷列表（对应 virsh pool-refresh）
	if active, _ := l.StoragePoolIsActive(pool); active == 1 {
		_ = l.StoragePoolRefresh(pool, 0)
	}
	vols, _, err := l.StoragePoolListAllVolumes(pool, 1, 0)
	if err != nil {
		return nil, fmt.Errorf("枚举存储池 %s 的卷失败: %w", poolName, err)
	}

	refs := make(map[string][]string)
	for _, vol := range vols {
		volXML, err := l.StorageVolGetXMLDesc(vol, 0)
		if err != nil {
			continue
		}
		var vx struct {
			BackingStore struct {
				Path string `xml:"path"`
			} `xml:"backingStore"`
		}
		if err := xmlDecode(volXML, &vx); err != nil {
			continue
		}
		if parent := vx.BackingStore.Path; parent != "" {
			refs[parent] = append(refs[parent], vol.Name)
		}
	}
	return refs, nil
}

// GetPoolPath 返回存储池的目标路径（解析 pool XML 的 <target><path>）。
func (v *Virt) GetPoolPath(poolName string) (string, error) {
	l, err := v.getConn()
	if err != nil {
		return "", err
	}

	pool, err := l.StoragePoolLookupByName(poolName)
	if err != nil {
		return "", fmt.Errorf("存储池 %s 不存在: %w", poolName, err)
	}

	xmlstr, err := l.StoragePoolGetXMLDesc(pool, 0)
	if err != nil {
		return "", fmt.Errorf("获取存储池配置失败: %w", err)
	}

	// 解析 <target><path>xxx</path></target>
	var p struct {
		Target struct {
			Path string `xml:"path"`
		} `xml:"target"`
	}
	if err := xmlDecode(xmlstr, &p); err != nil {
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
		return nil, fmt.Errorf("枚举存储池失败（对应 virsh pool-list --all）: %w", err)
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
		return nil, fmt.Errorf("枚举存储池失败（对应 virsh pool-list --all）: %w", err)
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
		return nil, fmt.Errorf("存储池 %s 不存在: %w", poolName, err)
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

	// 卷列表：先刷新池（对应 virsh pool-refresh），再用 ListAllVolumes
	// （StoragePoolListVolumes 第二参数是 maxnames，传 0 会返回空列表——历史坑）
	if active == 1 {
		_ = l.StoragePoolRefresh(pool, 0)
	}
	vols, _, err := l.StoragePoolListAllVolumes(pool, 1, 0)
	if err == nil {
		info.VolCount = len(vols)
		info.Volumes = make([]VolInfo, 0, len(vols))
		for _, vol := range vols {
			vi := VolInfo{Name: vol.Name}
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

	xmlstr, err := buildDirPoolXML(name, path)
	if err != nil {
		return err
	}

	pool, err := l.StoragePoolDefineXML(xmlstr, 0)
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
		return fmt.Errorf("存储池 %s 不存在: %w", name, err)
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
		return fmt.Errorf("存储池 %s 不存在: %w", poolName, err)
	}

	if format == "" {
		format = volFormatQcow2
	}
	xmlstr, err := buildVolumeXML(volName, format, volUnitGiB, int64(capacityGB))
	if err != nil {
		return err
	}

	if _, err := l.StorageVolCreateXML(pool, xmlstr, 0); err != nil {
		return fmt.Errorf("创建存储卷失败: %w", err)
	}
	return nil
}
