package virt

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/digitalocean/go-libvirt"
)

// xmlEscape 将字符串转义为 XML 文本内容，防止 XML 注入。
func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// SnapshotInfo 快照详情（供前端展示；对应 virsh snapshot-list --details）。
type SnapshotInfo struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	CreationTime int64  `json:"creation_time"` // unix 秒
	State        string `json:"state"`         // 平台状态映射（复用 StateToPlatform）
}

// ListSnapshots 返回虚拟机快照详情列表（对应 virsh snapshot-list --details）。
// 逐个 DomainSnapshotGetXMLDesc 解析 <name>/<description>/<creationTime>/<state>，
// 单条解析失败跳过，不阻断整体。
func (v *Virt) ListSnapshots(domainName string) ([]SnapshotInfo, error) {
	l, err := v.getConn()
	if err != nil {
		return nil, err
	}
	dom, err := l.DomainLookupByName(domainName)
	if err != nil {
		return nil, fmt.Errorf("虚拟机 %s 不存在: %w", domainName, err)
	}

	num, err := l.DomainSnapshotNum(dom, 0)
	if err != nil {
		return nil, fmt.Errorf("获取快照数量失败: %w", err)
	}
	if num == 0 {
		return []SnapshotInfo{}, nil
	}

	names, err := l.DomainSnapshotListNames(dom, num, 0)
	if err != nil {
		return nil, fmt.Errorf("获取快照列表失败: %w", err)
	}

	infos := make([]SnapshotInfo, 0, len(names))
	for _, name := range names {
		info, err := v.snapshotInfo(l, dom, name)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// snapshotInfo 解析单条快照详情（对应 virsh snapshot-list --details 的单行）。
func (v *Virt) snapshotInfo(l *libvirt.Libvirt, dom libvirt.Domain, name string) (SnapshotInfo, error) {
	snap, err := l.DomainSnapshotLookupByName(dom, name, 0)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("快照 %s 不存在: %w", name, err)
	}
	xmlstr, err := l.DomainSnapshotGetXMLDesc(snap, 0)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("获取快照 %s XML 失败: %w", name, err)
	}

	// 解析快照 XML 中的 <name>/<description>/<creationTime>/<state>
	var s struct {
		Name         string `xml:"name"`
		Description  string `xml:"description"`
		CreationTime int64  `xml:"creationTime"`
		State        string `xml:"state"`
	}
	if err := xml.Unmarshal([]byte(xmlstr), &s); err != nil {
		return SnapshotInfo{}, fmt.Errorf("解析快照 %s XML 失败: %w", name, err)
	}

	info := SnapshotInfo{
		Name:         s.Name,
		Description:  s.Description,
		CreationTime: s.CreationTime,
		State:        "error",
	}
	if st, ok := snapshotStateToInt32(s.State); ok {
		info.State = StateToPlatform(st)
	}
	return info, nil
}

// snapshotStateToInt32 将快照 XML <state> 元素转换为 libvirt 域状态枚举 int32。
// libvirt 快照 XML 的 <state> 通常为状态名字符串（如 running / shutoff），
// 某些实现也会直接输出枚举数字，两种都兼容；转换失败返回 false。
func snapshotStateToInt32(state string) (int32, bool) {
	state = strings.TrimSpace(state)
	if state == "" {
		return 0, false
	}
	// 兼容数字形式（libvirt 域状态枚举值）
	if n, err := strconv.ParseInt(state, 10, 32); err == nil {
		return int32(n), true
	}
	var v int32
	switch state {
	case "nostate":
		v = int32(libvirt.DomainNostate)
	case "running":
		v = int32(libvirt.DomainRunning)
	case "blocked":
		v = int32(libvirt.DomainBlocked)
	case "paused":
		v = int32(libvirt.DomainPaused)
	case "shutdown":
		v = int32(libvirt.DomainShutdown)
	case "shutoff":
		v = int32(libvirt.DomainShutoff)
	case "crashed":
		v = int32(libvirt.DomainCrashed)
	case "pmsuspended":
		v = int32(libvirt.DomainPmsuspended)
	default:
		return 0, false
	}
	return v, true
}

// CreateSnapshot 创建虚拟机快照（对应 virsh snapshot-create-as），description 写入快照描述。
func (v *Virt) CreateSnapshot(domainName, snapName, description string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domainName)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domainName, err)
	}
	if description == "" {
		description = "via vmops platform"
	}

	xml := fmt.Sprintf(`<domainsnapshot>
  <name>%s</name>
  <description>%s</description>
</domainsnapshot>`, xmlEscape(snapName), xmlEscape(description))

	if _, err := l.DomainSnapshotCreateXML(dom, xml, 0); err != nil {
		return fmt.Errorf("创建快照失败: %w", err)
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
		return fmt.Errorf("虚拟机 %s 不存在: %w", domainName, err)
	}
	snap, err := l.DomainSnapshotLookupByName(dom, snapName, 0)
	if err != nil {
		return fmt.Errorf("快照 %s 不存在: %w", snapName, err)
	}
	if err := l.DomainSnapshotDelete(snap, libvirt.DomainSnapshotDeleteChildren); err != nil {
		return fmt.Errorf("删除快照失败: %w", err)
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
		return fmt.Errorf("虚拟机 %s 不存在: %w", domainName, err)
	}
	snap, err := l.DomainSnapshotLookupByName(dom, snapName, 0)
	if err != nil {
		return fmt.Errorf("快照 %s 不存在: %w", snapName, err)
	}
	if err := l.DomainRevertToSnapshot(snap, 0); err != nil {
		return fmt.Errorf("回滚快照失败: %w", err)
	}
	return nil
}
