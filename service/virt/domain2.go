package virt

import (
	"fmt"
)

// PauseDomain 暂停虚拟机（对应 virsh suspend）。
func (v *Virt) PauseDomain(domain string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	if err := l.DomainSuspend(dom); err != nil {
		return fmt.Errorf("暂停虚拟机失败（对应 virsh suspend）: %w", err)
	}
	return nil
}

// ResumeDomain 恢复已暂停的虚拟机（对应 virsh resume）。
func (v *Virt) ResumeDomain(domain string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	if err := l.DomainResume(dom); err != nil {
		return fmt.Errorf("恢复虚拟机失败（对应 virsh resume）: %w", err)
	}
	return nil
}

// SetAutostart 设置虚拟机开机自启（对应 virsh autostart，1 启用 / 0 禁用）。
func (v *Virt) SetAutostart(domain string, enabled bool) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	val := int32(0)
	if enabled {
		val = 1
	}
	if err := l.DomainSetAutostart(dom, val); err != nil {
		return fmt.Errorf("设置自动启动失败（对应 virsh autostart）: %w", err)
	}
	return nil
}

// GetAutostart 查询虚拟机是否开机自启（对应 virsh dominfo 的 Autostart 行）。
func (v *Virt) GetAutostart(domain string) (bool, error) {
	l, err := v.getConn()
	if err != nil {
		return false, err
	}
	dom, err := l.DomainLookupByName(domain)
	if err != nil {
		return false, fmt.Errorf("虚拟机 %s 不存在: %w", domain, err)
	}
	autostart, err := l.DomainGetAutostart(dom)
	if err != nil {
		return false, fmt.Errorf("查询自动启动失败（对应 virsh dominfo）: %w", err)
	}
	return autostart == 1, nil
}
