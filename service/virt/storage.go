package virt

import (
	"encoding/xml"
	"fmt"

	"github.com/digitalocean/go-libvirt"
)

// xmlDecodePath 使用标准库 encoding/xml 解析存储池 XML。
func xmlDecodePath(data string, v interface{}) error {
	return xml.Unmarshal([]byte(data), v)
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
		return "", fmt.Errorf("创建存储卷失败: %v", err)
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
		return fmt.Errorf("删除存储卷失败: %v", err)
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
		return "", fmt.Errorf("获取存储池配置失败: %v", err)
	}

	// 解析 <target><path>xxx</path></target>
	var p struct {
		Target struct {
			Path string `xml:"path"`
		} `xml:"target"`
	}
	if err := xmlDecodePath(xml, &p); err != nil {
		return "", fmt.Errorf("解析存储池路径失败: %v", err)
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