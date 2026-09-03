package virt

import (
	"fmt"

	"github.com/digitalocean/go-libvirt"
)

// ListSnapshots 返回虚拟机快照列表（对应 virsh snapshot-list）。
func (v *Virt) ListSnapshots(domainName string) ([]string, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}
	dom, err := l.DomainLookupByName(domainName)
	if err != nil {
		return nil, fmt.Errorf("虚拟机 %s 不存在: %v", domainName, err)
	}

	num, err := l.DomainSnapshotNum(dom, 0)
	if err != nil {
		return nil, fmt.Errorf("获取快照数量失败: %v", err)
	}
	if num == 0 {
		return []string{}, nil
	}

	names, err := l.DomainSnapshotListNames(dom, num, 0)
	if err != nil {
		return nil, fmt.Errorf("获取快照列表失败: %v", err)
	}
	return names, nil
}

// CreateSnapshot 创建虚拟机快照（对应 virsh snapshot-create-as）。
func (v *Virt) CreateSnapshot(domainName, snapName string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domainName)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %v", domainName, err)
	}

	xml := fmt.Sprintf(`<domainsnapshot>
  <name>%s</name>
  <description>via vmops platform</description>
</domainsnapshot>`, snapName)

	if _, err := l.DomainSnapshotCreateXML(dom, xml, 0); err != nil {
		return fmt.Errorf("创建快照失败: %v", err)
	}
	return nil
}

// DeleteSnapshot 删除虚拟机快照（对应 virsh snapshot-delete）。
func (v *Virt) DeleteSnapshot(domainName, snapName string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domainName)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %v", domainName, err)
	}
	snap, err := l.DomainSnapshotLookupByName(dom, snapName, 0)
	if err != nil {
		return fmt.Errorf("快照 %s 不存在: %v", snapName, err)
	}
	if err := l.DomainSnapshotDelete(snap, libvirt.DomainSnapshotDeleteChildren); err != nil {
		return fmt.Errorf("删除快照失败: %v", err)
	}
	return nil
}

// RevertSnapshot 回滚到指定快照（对应 virsh snapshot-revert）。
func (v *Virt) RevertSnapshot(domainName, snapName string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domainName)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %v", domainName, err)
	}
	snap, err := l.DomainSnapshotLookupByName(dom, snapName, 0)
	if err != nil {
		return fmt.Errorf("快照 %s 不存在: %v", snapName, err)
	}
	if err := l.DomainRevertToSnapshot(snap, 0); err != nil {
		return fmt.Errorf("回滚快照失败: %v", err)
	}
	return nil
}