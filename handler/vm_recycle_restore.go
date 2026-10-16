// 本文件负责回收站「恢复」场景下的域定义重建：域已 undefine 时，优先按删除时留存的域
// 定义存档精确 define 回去（多盘/固件/光驱/原网络配置全还原），无存档或存档重建失败时
// 退回按 DB 记录精简重建（单系统盘 + 默认 NAT 网卡）。回收站列表/恢复/清除入口见 vm_recycle.go，
// 彻底清除见 vm_recycle_purge.go。
package handler

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// restoreOutcome 恢复重建的结果方式（B）：决定回给前端的文案与提示强度。
type restoreOutcome struct {
	Mode    string   // exact=按存档精确重建 / exact_missing=按存档重建但部分磁盘文件缺失 / minimal=无存档，精简重建
	Missing []string // 存档引用但文件已不存在的磁盘（仅 exact_missing）
}

// missingDiskFiles 从存档 XML 里找出「引用的磁盘文件已不存在」的路径（B）。
// 只统计 device=disk 的真实数据盘：光驱/软驱的 ISO 缺失不影响开机，不算缺失。
func missingDiskFiles(xmlText string) []string {
	spec, err := virt.ParseDomainXML(xmlText)
	if err != nil || spec == nil {
		return nil
	}
	missing := []string{}
	for _, d := range spec.Disks {
		if d.Device != "disk" || d.Source == "" {
			continue
		}
		if _, err := os.Stat(d.Source); err != nil {
			missing = append(missing, d.Source)
		}
	}
	return missing
}

// redefineRestoredVM 恢复场景下重建域定义并 define（对应 virsh define）。
// 两条路径（B）：
//  1. exact —— 删除时留存了域定义存档（VM.DomainXML）时原样 define 回去，
//     多盘/固件/光驱/原网络配置全部还原；存档引用但已丢失的磁盘单独列出告知。
//  2. minimal —— 无存档（老数据）或存档重建失败时，退回按 DB 记录重建精简定义：
//     单系统盘（按建卷命名约定探测，与 execDeleteVM 兜底同一套）+ 默认 NAT 单网卡；
//     多盘 VM 其余盘仍在池中，可在磁盘管理手动挂回。
func (h *VMRecycleHandler) redefineRestoredVM(vm model.VM) (restoreOutcome, error) {
	// 路径一：按存档精确重建
	if strings.TrimSpace(vm.DomainXML) != "" {
		missing := missingDiskFiles(vm.DomainXML)
		if derr := h.Virt.DefineDomain(vm.DomainXML); derr == nil {
			if len(missing) > 0 {
				log.Printf("[recycle] 按存档重建成功但有磁盘缺失 vm=%s missing=%v", vm.Name, missing)
				return restoreOutcome{Mode: "exact_missing", Missing: missing}, nil
			}
			return restoreOutcome{Mode: "exact"}, nil
		} else {
			// 存档可能来自旧版本/已被手工改动；退回精简重建而不是让恢复失败
			log.Printf("[recycle] 按域定义存档重建失败，退回精简重建 vm=%s err=%v", vm.Name, derr)
		}
	}

	// 路径二：精简重建（原逻辑）
	diskPath, diskPool := h.systemDiskPath(vm)
	if diskPath == "" {
		return restoreOutcome{}, fmt.Errorf("未找到 %s 的系统盘卷（记录池位 %q 及各池均未命中），无法重建定义", vm.Name, vm.StoragePool)
	}
	if vm.StoragePool != "" && diskPool != vm.StoragePool {
		// 盘位漂移留痕：能恢复（按绝对路径重建），但记录与实际不符值得知道
		log.Printf("[recycle] 系统盘不在记录池位 vm=%s 记录=%s 实际=%s", vm.Name, vm.StoragePool, diskPool)
	}

	vcpu := vm.VCPU
	if vcpu <= 0 {
		vcpu = 1
	}
	memMB := vm.MemoryMB
	if memMB <= 0 {
		memMB = 1024
	}
	spec := &virt.DomainSpec{
		Name:     vm.Name,
		UUID:     vm.UUID,
		VCPU:     vcpu,
		MemoryMB: memMB,
		OSType:   "hvm",
		Arch:     "x86_64",
		Boot:     virt.BootSpec{Devices: []string{"hd"}},
		Graphics: virt.GraphicsSpec{Type: "vnc", Port: -1},
	}
	spec.Disks = append(spec.Disks, virt.DiskSpec{
		Type: "file", Device: "disk", Driver: "qcow2", Bus: "virtio",
		Source: diskPath, Target: "vda",
	})
	mac := vm.MACAddress
	if mac == "" {
		// DB 未登记 MAC 时重新生成，避免 DB 记录与 libvirt 实际不一致
		m, merr := virt.RandomMAC()
		if merr != nil {
			return restoreOutcome{}, fmt.Errorf("生成网卡 MAC 失败: %w", merr)
		}
		mac = m
		if err := h.DB.Model(&vm).Update("mac_address", mac).Error; err != nil {
			// define 还没发生，这里失败仅留痕（MAC 兜底不一致不影响定义）
			log.Printf("[recycle] 重建定义前回写 MAC 失败 vm=%s err=%v", vm.Name, err)
		}
	}
	spec.Interfaces = append(spec.Interfaces, virt.InterfaceSpec{
		Type: "network", Source: "default", MAC: mac, Model: "virtio",
	})

	xmlstr, err := virt.BuildDomainXML(spec)
	if err != nil {
		return restoreOutcome{}, fmt.Errorf("重建虚拟机 %s 配置失败: %w", vm.Name, err)
	}
	if err := h.Virt.DefineDomain(xmlstr); err != nil {
		return restoreOutcome{}, fmt.Errorf("重新定义虚拟机 %s 失败: %w", vm.Name, err)
	}
	return restoreOutcome{Mode: "minimal"}, nil
}
