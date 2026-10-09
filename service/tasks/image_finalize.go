// image_finalize.go：模板制作 / 镜像清洗 executor——把「装好系统的制作机」固化成模板。
//
// 等价于用户手工流程（Virt02 模版机制作）：
//
//	virt-sysprep 清洗 → virt-customize 注入基础优化 → virt-sparsify 压缩 →
//	undefine 域 + 移除制作机记录 → 登记为模板（images 表 is_template）。
//
// **顺序即安全**：清洗/压缩成功后才动 VM 与登记；任一步失败保留制作机可重试
// （sysprep 本身不可逆，但 VM 还在、可重跑）。
// 约束：executor 运行在 worker goroutine，禁止引用 gin/handler；错误一律
// fmt.Errorf("中文描述: %w", err) 保留错误链。
package tasks

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/guestfs"
	"gorm.io/gorm"
)

// RegisterImageFinalize 注册镜像清洗 / 模板固化 executor。
func RegisterImageFinalize(m *Manager) {
	if m == nil {
		return
	}
	m.Register("image_finalize", execImageFinalize)
}

// execImageFinalize 把一台关机的「制作机」清洗固化为模板。
// payload：{vm_id*, name?, description?, os_version?, optimize?(默认 true), sparsify?(默认 true)}。
func execImageFinalize(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}
	vmID, ok := intParam(ctx.Payload, "vm_id")
	if !ok || vmID <= 0 {
		return errors.New("缺少虚拟机 ID 参数")
	}
	name, _ := strParam(ctx.Payload, "name")
	desc, _ := strParam(ctx.Payload, "description")
	osVer, _ := strParam(ctx.Payload, "os_version")
	optimize, hasOpt := boolParam(ctx.Payload, "optimize")
	if !hasOpt {
		optimize = true // 默认注入基础优化（让模板克隆即用）
	}
	sparsify, hasSp := boolParam(ctx.Payload, "sparsify")
	if !hasSp {
		sparsify = false // 默认不压缩：virt-sparsify --in-place 对 qcow2 收益甚微（实测反略增），
		// 且耗时长（全盘重写）——主要价值在 raw/预分配盘，故改为显式开启
	}

	var vm model.VM
	if err := ctx.DB.First(&vm, uint(vmID)).Error; err != nil {
		return fmt.Errorf("虚拟机不存在: %w", err)
	}
	// 1) 必须关机（清洗直接读写镜像文件，运行中会损坏磁盘——与离线挂载同口径）
	if vm.Status != model.VMStatusShutOff {
		return fmt.Errorf("虚拟机「%s」未关机，请先关机再固化为模板", vm.Name)
	}

	// 2) 定位系统盘：域 spec 第一块 device=disk 的盘；多盘直接拒绝（模板应只有系统盘）
	spec, err := ctx.Virt.GetDomainSpec(vm.Name)
	if err != nil {
		return fmt.Errorf("读取虚拟机磁盘清单失败: %w", err)
	}
	var disks []string
	for _, d := range spec.Disks {
		if d.Device == "disk" && d.Source != "" {
			disks = append(disks, d.Source)
		}
	}
	if len(disks) == 0 {
		return fmt.Errorf("虚拟机「%s」没有系统盘，无法固化为模板", vm.Name)
	}
	if len(disks) > 1 {
		return fmt.Errorf("虚拟机「%s」有 %d 块磁盘，模板制作机应只有一块系统盘（请先卸载数据盘再固化）", vm.Name, len(disks))
	}
	disk := disks[0]

	// 3) 工具可用性（真自检：二进制在但 libguestfs 起不来也要在这里挡住）
	if _, err := guestfs.Detect(); err != nil {
		return fmt.Errorf("镜像清洗工具不可用: %w", err)
	}
	// 4) 引用守卫：该盘不得被其它 VM 挂载 / 不得是其它卷的 backing 父盘
	if err := ensureDiskExclusive(ctx, disk, vm.Name); err != nil {
		return err
	}

	// 记录原属主/权限：guestfs（virt-sysprep/sparsify）经 sudo 以 root 运行会把文件改成
	// root:root，而 libvirt/qemu 需要 libvirt-qemu 可读——不还原则模板克隆出的 VM 起不来。
	restore := captureDiskOwner(disk)
	defer restore() // 任何退出路径（含失败早退）都还原属主/权限

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	// 5) virt-sysprep 清洗（去 SSH 主机密钥 / bash 历史 / machine-id / udev 持久网卡等）
	reportProgress(ctx, 10, "清洗镜像（virt-sysprep）…")
	if _, err := guestfs.Sysprep(baseCtx, disk, nil, guestfs.RunOpts{
		OnLine: func(line string) { log.Printf("[image-finalize] sysprep: %s", line) },
	}); err != nil {
		return fmt.Errorf("镜像清洗失败: %w", err)
	}

	// 6) virt-customize 注入基础优化（可选）
	if optimize {
		reportProgress(ctx, 45, "注入基础优化（qemu-guest-agent / cloud-init / 串口 console）…")
		scriptPath, serr := writeOptimizeScript()
		if serr != nil {
			return serr
		}
		defer func() { _ = os.Remove(scriptPath) }()
		if _, err := guestfs.Customize(baseCtx, disk, scriptPath, guestfs.RunOpts{
			OnLine: func(line string) { log.Printf("[image-finalize] customize: %s", line) },
		}); err != nil {
			return fmt.Errorf("注入基础优化失败: %w", err)
		}
	}

	// 压缩前占用（sysprep/customize 已写入完毕——统计 sparsify 的回收量才有意义）
	sizeBefore := diskSizeBytes(disk)

	// 7) virt-sparsify 原地压缩（可选）
	if sparsify {
		reportProgress(ctx, 62, "压缩镜像（virt-sparsify）…")
		if _, err := guestfs.Sparsify(baseCtx, disk, guestfs.RunOpts{
			OnLine: func(line string) { log.Printf("[image-finalize] sparsify: %s", line) },
		}); err != nil {
			return fmt.Errorf("压缩镜像失败: %w", err)
		}
	}
	sizeAfter := diskSizeBytes(disk)

	// 8) 清洗成功后才动 VM：移除域定义 + 硬删记录（不走回收站——磁盘已固化为模板，
	//    「恢复」出的 VM 会与模板共享同一块盘，后患无穷）
	reportProgress(ctx, 85, "移除制作机域定义与记录…")
	if err := ctx.Virt.UndefineDomain(vm.Name); err != nil {
		return fmt.Errorf("移除制作机域定义失败: %w", err)
	}
	if err := ctx.DB.Unscoped().Delete(&model.VM{}, vm.ID).Error; err != nil {
		return fmt.Errorf("移除制作机记录失败: %w", err)
	}
	// 关联数据随资产消亡（授权/托管凭据/主机组成员），失败只留痕不阻断
	if err := ctx.DB.Where("vm_id = ?", vm.ID).Delete(&model.VMGrant{}).Error; err != nil {
		log.Printf("[image-finalize] 回收授权失败 vm=%s err=%v", vm.Name, err)
	}
	if err := ctx.DB.Where("vm_id = ?", vm.ID).Delete(&model.VMGroupGrant{}).Error; err != nil {
		log.Printf("[image-finalize] 回收组授权失败 vm=%s err=%v", vm.Name, err)
	}
	if err := ctx.DB.Where("vm_id = ?", vm.ID).Delete(&model.VMCredential{}).Error; err != nil {
		log.Printf("[image-finalize] 清理托管凭据失败 vm=%s err=%v", vm.Name, err)
	}
	if err := removeVMFromHostGroups(ctx, vm.ID); err != nil {
		log.Printf("[image-finalize] 清理主机组成员失败 vm=%s err=%v", vm.Name, err)
	}

	// 9) 登记为模板（is_template=true）
	reportProgress(ctx, 92, "登记为模板镜像…")
	imgName := name
	if imgName == "" {
		imgName = vm.Name
	}
	img, err := registerFinalizedImage(ctx, disk, imgName, desc, osVer)
	if err != nil {
		return err
	}

	setTaskResultImage(ctx, map[string]interface{}{
		"image_id":    img.ID,
		"image_name":  img.Name,
		"path":        disk,
		"size_before": sizeBefore,
		"size_after":  sizeAfter,
		"saved_bytes": sizeBefore - sizeAfter,
		"optimized":   optimize,
		"sparsified":  sparsify,
	})
	return nil
}

// ensureDiskExclusive 守卫：目标盘不得被**其它** VM 挂载，也不得是任何卷的 backing 父盘。
// 自身（selfName）即将被移除，不算占用，故排除。
func ensureDiskExclusive(ctx *ExecContext, disk, selfName string) error {
	sources, err := ctx.Virt.ListAllDomainDiskSources()
	if err != nil {
		return fmt.Errorf("读取虚拟机磁盘挂载清单失败: %w", err)
	}
	for dom, paths := range sources {
		if dom == selfName {
			continue
		}
		for _, p := range paths {
			if p == disk {
				return fmt.Errorf("系统盘正被虚拟机「%s」挂载，无法固化为模板", dom)
			}
		}
	}
	for _, pool := range []string{"base", "images", "exten"} {
		backing, err := ctx.Virt.ListBackingRefs(pool)
		if err != nil {
			continue // 池不可用不阻断（保守取向与 imageRefs 一致）
		}
		if children, ok := backing[disk]; ok && len(children) > 0 {
			return fmt.Errorf("系统盘正被 %d 个增量克隆子卷当 backing 父盘，无法固化为模板", len(children))
		}
	}
	return nil
}

// diskSizeBytes 镜像文件实际占用字节（sparsify 压缩前后对比用；stat 失败返回 0）。
func diskSizeBytes(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

// captureDiskOwner 记录磁盘当前属主/权限，返回还原函数（no-op 表示未取到）。
// guestfs（virt-sysprep/virt-sparsify）经 sudo 以 root 运行会把文件改成 root:root，
// 而 libvirt/qemu 需要 libvirt-qemu 可读——不还原则模板克隆出的 VM 起不来。
func captureDiskOwner(path string) func() {
	st, err := os.Stat(path)
	if err != nil {
		return func() {}
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return func() {}
	}
	uid, gid, mode := sys.Uid, sys.Gid, st.Mode().Perm()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "sudo", "-n", "chown", fmt.Sprintf("%d:%d", uid, gid), path).Run()
		_ = exec.CommandContext(ctx, "sudo", "-n", "chmod", fmt.Sprintf("%04o", mode), path).Run()
	}
}

// writeOptimizeScript 把内置基础优化脚本写到临时文件（供 virt-customize --run 读取）。
func writeOptimizeScript() (string, error) {
	f, err := os.CreateTemp("", "vmops-optimize-*.sh")
	if err != nil {
		return "", fmt.Errorf("创建优化脚本临时文件失败: %w", err)
	}
	name := f.Name()
	if _, err := f.WriteString(guestfs.OptimizeScript()); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("写入优化脚本失败: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("关闭优化脚本失败: %w", err)
	}
	if err := os.Chmod(name, 0o755); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("设置优化脚本权限失败: %w", err)
	}
	return name, nil
}

// registerFinalizedImage 把清洗后的系统盘登记为模板镜像。
// 与 handler.RegisterImage 同语义（同路径去重 / 软删记录重新登记即恢复 / 活跃记录拒绝），
// 但 tasks 包禁 import handler，故此处自实现一份；两处语义必须同步。
func registerFinalizedImage(ctx *ExecContext, disk, name, desc, osVer string) (*model.Image, error) {
	st, err := os.Stat(disk)
	if err != nil {
		return nil, fmt.Errorf("读取镜像文件信息失败: %w", err)
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("系统盘路径不是常规文件，无法登记为镜像")
	}
	sizeGB := float64(st.Size()) / (1024.0 * 1024.0 * 1024.0)
	var img model.Image
	err = ctx.DB.Unscoped().Where("path = ?", disk).First(&img).Error
	switch {
	case err == nil && img.DeletedAt.Valid:
		// 软删记录：重新登记即恢复，并刷新为模板
		if uerr := ctx.DB.Unscoped().Model(&img).Updates(map[string]interface{}{
			"deleted_at":  nil,
			"name":        name,
			"description": desc,
			"os_version":  osVer,
			"is_template": true,
			"size_gb":     sizeGB,
		}).Error; uerr != nil {
			return nil, fmt.Errorf("恢复镜像登记失败: %w", uerr)
		}
		img.DeletedAt = gorm.DeletedAt{}
		img.Name = name
		img.Description = desc
		img.OSVersion = osVer
		img.IsTemplate = true
		img.SizeGB = sizeGB
	case err == nil:
		return nil, fmt.Errorf("该磁盘已登记为镜像「%s」，无法重复登记", img.Name)
	case errors.Is(err, gorm.ErrRecordNotFound):
		img = model.Image{
			Name:        name,
			Path:        disk,
			OSVersion:   osVer,
			SizeGB:      sizeGB,
			Format:      "qcow2",
			IsTemplate:  true,
			Description: desc,
		}
		if cerr := ctx.DB.Create(&img).Error; cerr != nil {
			return nil, fmt.Errorf("写入镜像记录失败: %w", cerr)
		}
	default:
		return nil, fmt.Errorf("查询镜像记录失败: %w", err)
	}
	return &img, nil
}
