package tasks

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// execCloneVM 克隆虚拟机（对应 virsh vol-clone + virsh define）。
// payload：{source_id*, name*, storage_pool, vcpu, memory_mb, network}。
// 克隆卷实际落在源系统盘所在存储池（CloneVMFromSpec 内部按源盘路径反查池建卷），
// DB storage_pool 同步记真实池：删除守卫按 vm.StoragePool 的池路径判定卷归属，
// 记成别的池会让子卷被判「池外」而保留成孤儿文件（实测踩中）。
func execCloneVM(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}
	payload := ctx.Payload

	sourceID, ok := intParam(payload, "source_id")
	if !ok || sourceID <= 0 {
		return errors.New("缺少源虚拟机 ID 参数")
	}
	name, ok := strParam(payload, "name")
	if !ok || name == "" {
		return errors.New("缺少虚拟机名称参数")
	}
	if !validateVMName(name) {
		return errors.New("虚拟机名称只允许字母、数字、下划线和连字符")
	}
	storagePool, _ := strParam(payload, "storage_pool")
	vcpu, _ := intParam(payload, "vcpu")
	memoryMB, _ := intParam(payload, "memory_mb")
	network, _ := strParam(payload, "network")

	var src model.VM
	if err := ctx.DB.First(&src, uint(sourceID)).Error; err != nil {
		return fmt.Errorf("源虚拟机不存在: %w", err)
	}
	reportProgress(ctx, 10, "开始克隆虚拟机")

	// 源 spec：可按需覆盖 vcpu/memory_mb/network（network 替换首个网卡 source）。
	source, err := ctx.Virt.GetDomainSpec(src.Name)
	if err != nil {
		return fmt.Errorf("获取源虚拟机配置失败: %w", err)
	}
	if source == nil {
		return errors.New("获取源虚拟机配置失败")
	}
	if vcpu > 0 {
		source.VCPU = vcpu
	}
	if memoryMB > 0 {
		source.MemoryMB = memoryMB
	}
	if network != "" && len(source.Interfaces) > 0 {
		source.Interfaces[0].Source = network
		source.Interfaces[0].Type = "network"
	}
	reportProgress(ctx, 30, "源配置读取完成，开始克隆磁盘")

	if _, err := ctx.Virt.CloneVMFromSpec(source, name); err != nil {
		return fmt.Errorf("克隆虚拟机失败: %w", err)
	}
	reportProgress(ctx, 50, "克隆完成（对应 virsh vol-clone + define）")

	// 新域 UUID 与首个网卡 MAC 回读 libvirt。
	// 注意：libvirt 不会自动改 MAC（XML 里显式给了就照用），重新生成是
	// CloneVMFromSpec 做的（randomMACAddr 逐块换），这里只是把结果同步进 DB。
	uuid := ""
	nicMAC := ""
	if ns, err := ctx.Virt.GetDomainSpec(name); err == nil && ns != nil {
		uuid = ns.UUID
		if len(ns.Interfaces) > 0 {
			nicMAC = ns.Interfaces[0].MAC
		}
	}
	if uuid == "" {
		uuid, err = virt.RandomUUID()
		if err != nil {
			return fmt.Errorf("生成虚拟机 UUID 失败: %w", err)
		}
	}

	// 克隆卷落在源系统盘所在池（CloneVMFromSpec 内部按源盘路径反查），
	// 这里用同一口径反查真实池写进 DB（与 virt 层各自反查一次，避免改 CloneVMFromSpec 签名）。
	// 多盘 VM 各盘可能分属不同池：按系统盘（首块 device=disk）所在池登记，注释即口径声明。
	clonePool := ""
	for _, d := range source.Disks {
		if d.Device == "disk" && d.Source != "" {
			if pn, _, lerr := ctx.Virt.LookupVolByPath(d.Source); lerr == nil {
				clonePool = pn
			}
			break
		}
	}
	if clonePool == "" {
		// 源盘反查不到所属池（池外盘等异常形态）时退回旧口径：请求池 → 源机记录池。
		clonePool = storagePool
		if clonePool == "" {
			clonePool = src.StoragePool
		}
	}
	clone := model.VM{
		UUID:        uuid,
		Name:        name,
		HostID:      src.HostID,
		StoragePool: clonePool,
		VCPU:        source.VCPU,
		MemoryMB:    source.MemoryMB,
		DiskGB:      src.DiskGB,
		MACAddress:  nicMAC,
		Status:      model.VMStatusShutOff,
	}
	if err := ctx.DB.Create(&clone).Error; err != nil {
		return fmt.Errorf("记录克隆虚拟机失败: %w", err)
	}
	reportProgress(ctx, 70, "克隆记录已落盘")
	// 克隆发起人自动获得克隆机的授权（失败留痕不阻断，理由同 execCreateVM）
	if ctx.Task.UserID != nil {
		if err := ctx.DB.Create(&model.VMGrant{UserID: *ctx.Task.UserID, VMID: clone.ID, GrantedBy: *ctx.Task.UserID}).Error; err != nil {
			log.Printf("[tasks] 克隆自动授权失败 vm=%s user=%d err=%v", clone.Name, *ctx.Task.UserID, err)
		}
	}

	setTaskResultVM(ctx, map[string]interface{}{"vm_id": clone.ID}, clone.ID, clone.Name)
	return nil
}

// execCloneImageVM 基于镜像/模板创建虚拟机（对应 virsh vol-clone + virsh define）。
// payload：{image_id*, name*, storage_pool, vcpu, memory_mb, network,
// cloud_init{hostname,user,password,ssh_key,net_mode,ip,gateway,dns}?}。
// 云镜像做 linked clone：子卷带 backing file（对应 virsh vol-clone），保护基镜像不被 VM 写入破坏。
func execCloneImageVM(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}
	payload := ctx.Payload

	imageID, ok := intParam(payload, "image_id")
	if !ok || imageID <= 0 {
		return errors.New("缺少镜像 ID 参数")
	}
	name, ok := strParam(payload, "name")
	if !ok || name == "" {
		return errors.New("缺少虚拟机名称参数")
	}
	if !validateVMName(name) {
		return errors.New("虚拟机名称只允许字母、数字、下划线和连字符")
	}
	vcpu, _ := intParam(payload, "vcpu")
	memoryMB, _ := intParam(payload, "memory_mb")
	network, _ := strParam(payload, "network")
	var cloudInit *virt.CloudInitSpec
	if raw, ok := payload["cloud_init"]; ok && raw != nil {
		cloudInit = parseTaskCloudInit(raw)
	}

	var img model.Image
	if err := ctx.DB.First(&img, uint(imageID)).Error; err != nil {
		return fmt.Errorf("镜像不存在: %w", err)
	}
	if vcpu == 0 {
		vcpu = 1
	}
	if memoryMB == 0 {
		memoryMB = 1024
	}
	if network == "" {
		network = "default"
	}
	reportProgress(ctx, 10, "开始基于镜像创建虚拟机")

	uuid, err := virt.RandomUUID()
	if err != nil {
		return fmt.Errorf("生成虚拟机 UUID 失败: %w", err)
	}
	mac, err := virt.RandomMAC()
	if err != nil {
		return fmt.Errorf("生成虚拟机 MAC 失败: %w", err)
	}

	// 基于云镜像做 linked clone：子卷带 backing file（对应 virsh vol-clone）。
	poolName, volName, err := ctx.Virt.LookupVolByPath(img.Path)
	if err != nil {
		return fmt.Errorf("定位镜像存储卷失败: %w", err)
	}
	newDiskName := name + "-sys"
	diskPath, err := ctx.Virt.CloneVolumeFromVol(poolName, volName, newDiskName)
	if err != nil {
		return fmt.Errorf("克隆镜像卷失败: %w", err)
	}
	reportProgress(ctx, 40, "镜像卷克隆完成（对应 virsh vol-clone）")

	// 失败清理：删克隆卷 + seed 文件 + 未定义域。
	cloneCleaned := false
	seedPath := ""
	cleanup := func() {
		if !cloneCleaned {
			_ = ctx.Virt.DeleteVolume(poolName, newDiskName+".qcow2")
		}
		if seedPath != "" {
			_ = os.Remove(seedPath)
		}
		_ = ctx.Virt.UndefineDomain(name)
	}

	var host model.Host
	if err := ctx.DB.Order("id ASC").First(&host).Error; err != nil {
		cleanup()
		cloneCleaned = true
		return fmt.Errorf("请先在宿主机管理中登记宿主机: %w", err)
	}

	spec := &virt.DomainSpec{
		Name:     name,
		UUID:     uuid,
		VCPU:     vcpu,
		MemoryMB: memoryMB,
		OSType:   "hvm",
		Arch:     "x86_64",
		Boot:     virt.BootSpec{Devices: []string{"hd"}},
		Graphics: virt.GraphicsSpec{Type: "vnc", Port: -1},
	}
	spec.Disks = append(spec.Disks, virt.DiskSpec{
		Type: "file", Device: "disk", Driver: "qcow2", Bus: "virtio",
		Source: diskPath, Target: "vda",
	})
	spec.Interfaces = append(spec.Interfaces, virt.InterfaceSpec{
		Type: "network", Source: network, MAC: mac, Model: "virtio",
	})

	// cloud-init：生成 seed ISO 落到 seed 目录，挂只读 cdrom。
	if cloudInit != nil {
		if cloudInit.Hostname == "" {
			cloudInit.Hostname = name
		}
		seedBytes, err := virt.GenerateSeedISO(cloudInit)
		if err != nil {
			cleanup()
			cloneCleaned = true
			return fmt.Errorf("生成 cloud-init seed 失败: %w", err)
		}
		// seed 写到独立 seed 目录（web 可写、qemu 可读），不依赖池目录权限。
		seedDir := taskSeedDir()
		if err := os.MkdirAll(seedDir, 0755); err != nil {
			cleanup()
			cloneCleaned = true
			return fmt.Errorf("创建 cloud-init seed 目录失败: %w", err)
		}
		seedPath = filepath.Join(seedDir, name+"-seed.iso")
		if err := os.WriteFile(seedPath, seedBytes, 0644); err != nil {
			cleanup()
			cloneCleaned = true
			return fmt.Errorf("写入 cloud-init seed 镜像失败: %w", err)
		}
		spec.Disks = append(spec.Disks, virt.DiskSpec{
			Type: "file", Device: "cdrom", Driver: "raw", Bus: "ide",
			Source: seedPath, ReadOnly: true, Target: "hda",
		})
		spec.Boot.Devices = []string{"cdrom", "hd"}
	}
	reportProgress(ctx, 70, "系统盘与 seed 准备完成，正在定义域")

	xmlstr, err := virt.BuildDomainXML(spec)
	if err != nil {
		cleanup()
		cloneCleaned = true
		return fmt.Errorf("虚拟机配置不合法: %w", err)
	}
	if err := ctx.Virt.DefineDomain(xmlstr); err != nil {
		cleanup()
		cloneCleaned = true
		return fmt.Errorf("定义虚拟机失败: %w", err)
	}

	vm := model.VM{
		UUID:        uuid,
		Name:        name,
		HostID:      host.ID,
		StoragePool: poolName, // 实际克隆卷落在镜像所在池
		VCPU:        vcpu,
		MemoryMB:    memoryMB,
		DiskGB:      int(img.SizeGB + 0.5),
		MACAddress:  mac,
		Status:      model.VMStatusShutOff,
	}
	if err := ctx.DB.Create(&vm).Error; err != nil {
		cleanup()
		cloneCleaned = true
		return fmt.Errorf("记录虚拟机失败: %w", err)
	}
	cloneCleaned = true // 创建成功，保留克隆卷

	// 创建者自动获得该 VM 的授权（失败留痕不阻断，理由同 execCreateVM）
	if ctx.Task.UserID != nil {
		if err := ctx.DB.Create(&model.VMGrant{UserID: *ctx.Task.UserID, VMID: vm.ID, GrantedBy: *ctx.Task.UserID}).Error; err != nil {
			log.Printf("[tasks] 建机自动授权失败 vm=%s user=%d err=%v", vm.Name, *ctx.Task.UserID, err)
		}
	}

	setTaskResultVM(ctx, map[string]interface{}{"vm_id": vm.ID}, vm.ID, vm.Name)
	return nil
}
