package handler

import (
	"testing"

	"github.com/jiuzhao/vmops/service/virt"
)

// TestMissingDiskSources 幽灵盘探测：只把「数据盘（device=disk）文件缺失」判为致命，
// 光驱 ISO 缺失不致命——libvirt 对缺失的数据盘会拒绝启动整个域，对光驱只是没介质。
func TestMissingDiskSources(t *testing.T) {
	spec := &virt.DomainSpec{
		Name: "demo",
		Disks: []virt.DiskSpec{
			{Device: "disk", Target: "vda", Source: "/etc/hostname"},           // 存在
			{Device: "disk", Target: "vdb", Source: "/nonexistent/gone.qcow2"}, // 缺失 → 致命
			{Device: "cdrom", Target: "sda", Source: "/nonexistent/gone.iso"},  // 缺失但不致命
		},
	}
	missing, fatal := missingDiskSources(spec)
	if len(missing) != 2 {
		t.Fatalf("应报 2 项缺失（1 数据盘 + 1 光驱），实际 %d：%v", len(missing), missing)
	}
	if !fatal {
		t.Error("含缺失数据盘时应判为致命（虚拟机无法开机）")
	}
	var diskEntry, cdEntry map[string]interface{}
	for _, m := range missing {
		if m["device"] == "disk" {
			diskEntry = m
		} else {
			cdEntry = m
		}
	}
	if diskEntry == nil || diskEntry["fatal"] != true || diskEntry["target"] != "vdb" {
		t.Errorf("数据盘缺失项异常：%+v", diskEntry)
	}
	if cdEntry == nil || cdEntry["fatal"] != false {
		t.Errorf("光驱缺失项不应判致命：%+v", cdEntry)
	}

	// 只有光驱缺失 → 不致命
	onlyCD := &virt.DomainSpec{Disks: []virt.DiskSpec{
		{Device: "cdrom", Target: "sda", Source: "/nonexistent/gone.iso"},
	}}
	if m, f := missingDiskSources(onlyCD); f || len(m) != 1 {
		t.Errorf("仅光驱缺失应不致命且有 1 项，实际 fatal=%v items=%v", f, m)
	}

	// 全部存在 → 无缺失
	allOK := &virt.DomainSpec{Disks: []virt.DiskSpec{{Device: "disk", Target: "vda", Source: "/etc/hostname"}}}
	if m, f := missingDiskSources(allOK); f || len(m) != 0 {
		t.Errorf("全部存在时不应报缺失，实际 fatal=%v items=%v", f, m)
	}

	// 空 source（占位/未指定介质）跳过，不误报
	empty := &virt.DomainSpec{Disks: []virt.DiskSpec{{Device: "cdrom", Target: "sda", Source: ""}}}
	if m, f := missingDiskSources(empty); f || len(m) != 0 {
		t.Errorf("空 source 不应计入缺失，实际 %v", m)
	}

	// nil spec 不 panic
	if m, f := missingDiskSources(nil); f || len(m) != 0 {
		t.Errorf("nil spec 应返回空，实际 %v", m)
	}
}
