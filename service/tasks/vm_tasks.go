// Package tasks 内放后台任务执行器（executor）注册逻辑。
//
// 本文件实现 5 个 VM 异步任务 executor（对应 virsh 耗时操作后台化）：
// create_vm / delete_vm / clone_vm / clone_image_vm / stop_vm。
// 业务逻辑分别从 handler/vm.go（CreateVM/DeleteVM/CloneVM/StopVM）与
// handler/image.go（CloneVM）搬运而来，h.DB/h.Virt 改为 ctx.DB/ctx.Virt。
//
// 约束：executor 运行在 worker goroutine，禁止引用 gin/handler；
// 所有错误用 fmt.Errorf("中文描述: %w", err) 保留错误链。
package tasks

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// taskVMNameRegex 虚拟机名称只允许字母、数字、下划线和连字符（copy 自 handler/vm.go）。
var taskVMNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateVMName 校验虚拟机名称合法性（copy 自 handler/vm.go，tasks 包内自实现）。
func validateVMName(name string) bool {
	return taskVMNameRegex.MatchString(name)
}

// randomMAC 生成一个 52:54:00:xx:xx:xx 格式的 KVM 默认 MAC 地址（copy 自 handler/vm.go）。
func randomMAC() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[0], b[1], b[2]), nil
}

// randomUUID 生成一个符合 RFC 4122 的 v4 UUID 字符串（copy 自 handler/vm.go）。
func randomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	), nil
}

// strParam 从 payload 安全取字符串（类型断言带 ok，缺失/类型不符返回 false）。
func strParam(payload map[string]interface{}, key string) (string, bool) {
	if payload == nil {
		return "", false
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

// floatParam 从 payload 安全取数值（兼容 JSON 反序列化的 float64 与 Submit 直传的整型）。
func floatParam(payload map[string]interface{}, key string) (float64, bool) {
	if payload == nil {
		return 0, false
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// intParam 从 payload 安全取整数。
func intParam(payload map[string]interface{}, key string) (int, bool) {
	f, ok := floatParam(payload, key)
	if !ok {
		return 0, false
	}
	return int(f), true
}

// createDiskReq 创建 VM 时的磁盘描述：三选一
// (1) create_gb 新建卷；(2) source 直接引用现有卷/镜像路径；(3) source_image_id 引用云镜像（DB images.id）。
type createDiskReq struct {
	CreateGB      int
	Source        string
	SourceImageID uint
	CloudInit     *virt.CloudInitSpec
}

// parseTaskCloudInit 从 payload 子项安全解析 cloud-init 配置（非 map 时返回 nil）。
func parseTaskCloudInit(v interface{}) *virt.CloudInitSpec {
	m, ok := v.(map[string]interface{})
	if !ok || m == nil {
		return nil
	}
	spec := &virt.CloudInitSpec{}
	if s, ok := strParam(m, "hostname"); ok {
		spec.Hostname = s
	}
	if s, ok := strParam(m, "user"); ok {
		spec.User = s
	}
	if s, ok := strParam(m, "password"); ok {
		spec.Password = s
	}
	if s, ok := strParam(m, "ssh_key"); ok {
		spec.SSHKey = s
	}
	if s, ok := strParam(m, "net_mode"); ok {
		spec.NetMode = s
	}
	if s, ok := strParam(m, "ip"); ok {
		spec.IP = s
	}
	if s, ok := strParam(m, "gateway"); ok {
		spec.Gateway = s
	}
	if raw, ok := m["dns"]; ok && raw != nil {
		if arr, ok := raw.([]interface{}); ok {
			dns := make([]string, 0, len(arr))
			for _, e := range arr {
				if s, ok := e.(string); ok && s != "" {
					dns = append(dns, s)
				}
			}
			spec.DNS = dns
		}
	}
	return spec
}

// parseTaskDisks 从 payload 安全解析磁盘列表。
func parseTaskDisks(v interface{}) []createDiskReq {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]createDiskReq, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]interface{})
		if !ok || m == nil {
			continue
		}
		var d createDiskReq
		if n, ok := intParam(m, "create_gb"); ok {
			d.CreateGB = n
		}
		if s, ok := strParam(m, "source"); ok {
			d.Source = s
		}
		if n, ok := intParam(m, "source_image_id"); ok && n > 0 {
			d.SourceImageID = uint(n)
		}
		if ci, ok := m["cloud_init"]; ok && ci != nil {
			d.CloudInit = parseTaskCloudInit(ci)
		}
		out = append(out, d)
	}
	return out
}

// parseTaskInterfaces 从 payload 安全解析网卡列表。
func parseTaskInterfaces(v interface{}) []virt.InterfaceSpec {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]virt.InterfaceSpec, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]interface{})
		if !ok || m == nil {
			continue
		}
		var spec virt.InterfaceSpec
		if s, ok := strParam(m, "type"); ok {
			spec.Type = s
		}
		if s, ok := strParam(m, "source"); ok {
			spec.Source = s
		}
		if s, ok := strParam(m, "mac"); ok {
			spec.MAC = s
		}
		if s, ok := strParam(m, "model"); ok {
			spec.Model = s
		}
		out = append(out, spec)
	}
	return out
}

// firstTaskHost 返回平台登记的首台宿主机（默认纳管目标，copy 自 handler firstHost）。
func firstTaskHost(ctx *ExecContext) (*model.Host, error) {
	var host model.Host
	if err := ctx.DB.Order("id ASC").First(&host).Error; err != nil {
		return nil, err
	}
	return &host, nil
}

// taskSeedDir 返回 cloud-init seed 落盘目录（带默认值，防御 GlobalConfig 未初始化）。
func taskSeedDir() string {
	if config.GlobalConfig != nil && config.GlobalConfig.SeedDir != "" {
		return config.GlobalConfig.SeedDir
	}
	return "/home/jiuzhao/vmops/data/seed"
}

// checkExecContext 校验 executor 上下文（worker 内仅可访问 ctx.Task/DB/Virt）。
func checkExecContext(ctx *ExecContext) error {
	if ctx == nil {
		return errors.New("任务上下文为空")
	}
	if ctx.DB == nil {
		return errors.New("任务数据库连接为空")
	}
	if ctx.Virt == nil {
		return errors.New("任务虚拟化服务为空")
	}
	if ctx.Task == nil {
		return errors.New("任务记录为空")
	}
	if ctx.Payload == nil {
		return errors.New("缺少任务参数")
	}
	return nil
}

// reportProgress 上报进度（Report 为空时忽略，防御 manager 未注入）。
func reportProgress(ctx *ExecContext, pct int, msg string) {
	if ctx != nil && ctx.Report != nil {
		ctx.Report(pct, msg)
	}
}

// setTaskResultVM 回填任务结果与 VM 关联（Result 为 JSON 字符串）。
func setTaskResultVM(ctx *ExecContext, result map[string]interface{}, vmID uint, vmName string) {
	if b, err := json.Marshal(result); err == nil {
		ctx.Task.Result = string(b)
	}
	id := vmID
	ctx.Task.VMID = &id
	ctx.Task.VMName = vmName
}

// RegisterVMTasks 注册 5 个 VM 任务 executor（契约 service/tasks/vm_tasks.go，T2 产出）。
func RegisterVMTasks(m *Manager) {
	if m == nil {
		return
	}
	m.Register("create_vm", execCreateVM)
	m.Register("delete_vm", execDeleteVM)
	m.Register("clone_vm", execCloneVM)
	m.Register("clone_image_vm", execCloneImageVM)
	m.Register("stop_vm", execStopVM)
}

// execCreateVM 创建虚拟机（对应 virsh vol-create-as + virsh define）。
// payload 复刻原 CreateVM 请求体：
// {name*, host_id, template, storage_pool, vcpu, memory_mb, disk_gb,
//
//	disks[{create_gb,source,source_image_id,cloud_init}], interfaces[{type,source,mac,model}],
//	network, iso_path, cloud_init{hostname,user,password,ssh_key,net_mode,ip,gateway,dns}}。
func execCreateVM(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}
	payload := ctx.Payload

	name, ok := strParam(payload, "name")
	if !ok || name == "" {
		return errors.New("缺少虚拟机名称参数")
	}
	if !validateVMName(name) {
		return errors.New("虚拟机名称只允许字母、数字、下划线和连字符")
	}

	template, _ := strParam(payload, "template")
	storagePool, _ := strParam(payload, "storage_pool")
	network, _ := strParam(payload, "network")
	isoPath, _ := strParam(payload, "iso_path")
	vcpu, _ := intParam(payload, "vcpu")
	memoryMB, _ := intParam(payload, "memory_mb")
	diskGB, _ := intParam(payload, "disk_gb")
	hostID, hasHostID := intParam(payload, "host_id")

	var disks []createDiskReq
	if raw, ok := payload["disks"]; ok && raw != nil {
		disks = parseTaskDisks(raw)
	}
	var interfaces []virt.InterfaceSpec
	if raw, ok := payload["interfaces"]; ok && raw != nil {
		interfaces = parseTaskInterfaces(raw)
	}
	var cloudInit *virt.CloudInitSpec
	if raw, ok := payload["cloud_init"]; ok && raw != nil {
		cloudInit = parseTaskCloudInit(raw)
	}

	// 查宿主机：未指定时取平台登记的首台。
	var host model.Host
	if hasHostID && hostID != 0 {
		if err := ctx.DB.First(&host, uint(hostID)).Error; err != nil {
			return fmt.Errorf("宿主机不存在: %w", err)
		}
	} else {
		hst, err := firstTaskHost(ctx)
		if err != nil {
			return fmt.Errorf("请先在宿主机管理中登记宿主机: %w", err)
		}
		host = *hst
	}

	// 默认值（沿用原 CreateVM 逻辑）。
	if storagePool == "" {
		storagePool = "vmops"
	}
	if vcpu == 0 {
		vcpu = 1
	}
	if memoryMB == 0 {
		memoryMB = 1024
	}
	if network == "" && len(interfaces) == 0 {
		network = "default"
	}
	// 旧调用兼容：未提供 disks 时按 disk_gb 建默认盘。
	if len(disks) == 0 {
		gb := diskGB
		if gb == 0 {
			gb = 20
		}
		disks = []createDiskReq{{CreateGB: gb}}
	}

	// 生成 UUID 与首个网卡 MAC。
	uuid, err := randomUUID()
	if err != nil {
		return fmt.Errorf("生成虚拟机 UUID 失败: %w", err)
	}
	firstMAC, err := randomMAC()
	if err != nil {
		return fmt.Errorf("生成虚拟机 MAC 失败: %w", err)
	}

	// 组装 DomainSpec。
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

	// 记录已建卷（pool:volName），失败时回滚清理。
	var createdVols []string
	totalDiskGB := 0
	seedPath := ""
	cleanup := func() {
		for _, cv := range createdVols {
			parts := strings.SplitN(cv, ":", 2)
			if len(parts) == 2 {
				_ = ctx.Virt.DeleteVolume(parts[0], parts[1])
			}
		}
		if seedPath != "" {
			_ = os.Remove(seedPath)
		}
	}

	// 逐磁盘落地：create_gb → 建卷；source → 直接引用；source_image_id → 引用云镜像文件。
	reportProgress(ctx, 10, "开始创建虚拟机磁盘")
	poolPath := ""
	for i, d := range disks {
		var source string
		switch {
		case d.CreateGB > 0:
			volName := name
			if i > 0 {
				volName = fmt.Sprintf("%s-d%d", name, i+1)
			}
			if _, err := ctx.Virt.CreateVolume(storagePool, volName, d.CreateGB); err != nil {
				cleanup()
				return fmt.Errorf("创建磁盘卷失败: %w", err)
			}
			createdVols = append(createdVols, storagePool+":"+volName+".qcow2")
			if poolPath == "" {
				if poolPath, err = ctx.Virt.GetPoolPath(storagePool); err != nil {
					cleanup()
					return fmt.Errorf("获取存储池路径失败: %w", err)
				}
			}
			source = filepath.Join(poolPath, volName+".qcow2")
			totalDiskGB += d.CreateGB
		case d.Source != "":
			source = d.Source
		case d.SourceImageID > 0:
			var img model.Image
			if err := ctx.DB.First(&img, d.SourceImageID).Error; err != nil {
				cleanup()
				return fmt.Errorf("云镜像不存在: %w", err)
			}
			// 云镜像直接引用，不拷贝：VM 与镜像共用文件，镜像删除前需先删引用 VM。
			source = img.Path
		default:
			cleanup()
			return errors.New("磁盘参数不完整（create_gb / source / source_image_id 三选一）")
		}
		spec.Disks = append(spec.Disks, virt.DiskSpec{
			Type:   "file",
			Device: "disk",
			Driver: "qcow2",
			Bus:    "virtio",
			Source: source,
			Target: virt.NextDiskTarget(spec, "virtio"),
		})
		if len(disks) > 0 {
			reportProgress(ctx, 10+20*(i+1)/len(disks), "磁盘创建中")
		}
	}
	reportProgress(ctx, 30, "磁盘落地完成")

	// 兼容旧 iso_path：挂只读 cdrom 安装盘，引导优先光驱。
	if isoPath != "" {
		spec.Disks = append(spec.Disks, virt.DiskSpec{
			Type: "file", Device: "cdrom", Driver: "raw", Bus: "ide",
			Source: isoPath, ReadOnly: true,
			Target: virt.NextDiskTarget(spec, "ide"),
		})
		spec.Boot.Devices = []string{"cdrom", "hd"}
	}

	// cloud-init：顶层缺失时从磁盘项中查找，生成 seed ISO 落到 seed 目录，挂为只读 cdrom。
	cfg := cloudInit
	if cfg == nil {
		for i := range disks {
			if disks[i].CloudInit != nil {
				cfg = disks[i].CloudInit
				break
			}
		}
	}
	if cfg != nil {
		if cfg.Hostname == "" {
			cfg.Hostname = name
		}
		seedBytes, err := virt.GenerateSeedISO(cfg)
		if err != nil {
			cleanup()
			return fmt.Errorf("生成 cloud-init seed 失败: %w", err)
		}
		// seed 写到独立 seed 目录（web 可写、qemu 可读），避免依赖存储池目录权限。
		seedDir := taskSeedDir()
		if err := os.MkdirAll(seedDir, 0755); err != nil {
			cleanup()
			return fmt.Errorf("创建 cloud-init seed 目录失败: %w", err)
		}
		seedPath = filepath.Join(seedDir, name+"-seed.iso")
		if err := os.WriteFile(seedPath, seedBytes, 0644); err != nil {
			cleanup()
			return fmt.Errorf("写入 cloud-init seed 镜像失败: %w", err)
		}
		spec.Disks = append(spec.Disks, virt.DiskSpec{
			Type: "file", Device: "cdrom", Driver: "raw", Bus: "ide",
			Source: seedPath, ReadOnly: true,
			Target: virt.NextDiskTarget(spec, "ide"),
		})
		spec.Boot.Devices = []string{"cdrom", "hd"}
	}

	// 网卡：未显式提供 interfaces 时按 network 便捷字段生成。
	nicMAC := firstMAC
	if len(interfaces) > 0 {
		for i := range interfaces {
			if interfaces[i].Type == "" {
				interfaces[i].Type = "network"
			}
			if interfaces[i].Source == "" {
				interfaces[i].Source = network
				if interfaces[i].Source == "" {
					interfaces[i].Source = "default"
				}
			}
			if interfaces[i].MAC == "" {
				m, err := randomMAC()
				if err != nil {
					cleanup()
					return fmt.Errorf("生成网卡 MAC 失败: %w", err)
				}
				interfaces[i].MAC = m
			}
			if interfaces[i].Model == "" {
				interfaces[i].Model = "virtio"
			}
			if i == 0 {
				nicMAC = interfaces[i].MAC
			}
			spec.Interfaces = append(spec.Interfaces, interfaces[i])
		}
	} else {
		spec.Interfaces = append(spec.Interfaces, virt.InterfaceSpec{
			Type: "network", Source: network, MAC: firstMAC, Model: "virtio",
		})
	}
	reportProgress(ctx, 50, "网络与 cloud-init 配置完成")

	// BuildDomainXML 生成完整定义（纯函数），再写 DB 记录与 define。
	xmlstr, err := virt.BuildDomainXML(spec)
	if err != nil {
		cleanup()
		return fmt.Errorf("虚拟机配置不合法: %w", err)
	}

	vm := model.VM{
		UUID:        uuid,
		Name:        name,
		HostID:      host.ID,
		Template:    template,
		StoragePool: storagePool,
		VCPU:        vcpu,
		MemoryMB:    memoryMB,
		DiskGB:      totalDiskGB,
		MACAddress:  nicMAC,
		Status:      "shut off",
	}
	if err := ctx.DB.Create(&vm).Error; err != nil {
		cleanup()
		return fmt.Errorf("创建虚拟机记录失败: %w", err)
	}
	reportProgress(ctx, 70, "虚拟机记录已落盘，正在定义域")
	if err := ctx.Virt.DefineDomain(xmlstr); err != nil {
		_ = ctx.DB.Delete(&vm)
		cleanup()
		return fmt.Errorf("定义虚拟机失败: %w", err)
	}

	setTaskResultVM(ctx, map[string]interface{}{"vm_id": vm.ID}, vm.ID, vm.Name)
	return nil
}

// execDeleteVM 删除虚拟机（对应 virsh undefine + virsh vol-delete）。
// payload：{vm_id*}。
func execDeleteVM(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}

	vmID, ok := intParam(ctx.Payload, "vm_id")
	if !ok || vmID <= 0 {
		return errors.New("缺少虚拟机 ID 参数")
	}

	var vm model.VM
	if err := ctx.DB.First(&vm, uint(vmID)).Error; err != nil {
		return fmt.Errorf("虚拟机不存在: %w", err)
	}
	reportProgress(ctx, 10, "开始删除虚拟机")

	// 1. 先取完整磁盘清单（含多盘/克隆卷/seed 盘），再删除域定义
	//    （spec 解析失败不阻断删除，域仍按既有流程清理）。
	var diskSources []string
	if spec, err := ctx.Virt.GetDomainSpec(vm.Name); err == nil && spec != nil {
		for _, d := range spec.Disks {
			if d.Source != "" {
				diskSources = append(diskSources, d.Source)
			}
		}
	}

	// 2. 删除域定义（对应 virsh undefine）。
	if err := ctx.Virt.UndefineDomain(vm.Name); err != nil {
		return fmt.Errorf("删除虚拟机定义失败: %w", err)
	}
	reportProgress(ctx, 30, "虚拟机定义已删除（对应 virsh undefine），开始清理磁盘")

	// 3. 删除存储卷（对应 virsh vol-delete）：枚举的磁盘源 + 默认系统盘兜底。
	//    仅删除位于平台托管池路径下的卷，避免误删共享基镜像（如克隆子卷的父盘）。
	pool := vm.StoragePool
	if pool == "" {
		pool = "vmops"
	}
	// 池路径前缀（用于判定卷是否属于平台托管，避免删共享镜像）。
	poolPath, _ := ctx.Virt.GetPoolPath(pool)
	volNameFor := func(src string) (string, bool) {
		if src == "" {
			return "", false
		}
		if poolPath != "" && !strings.HasPrefix(src, poolPath+"/") {
			return "", false
		}
		return filepath.Base(src), true
	}
	seen := map[string]bool{}
	tryDeleteVol := func(src string) {
		if src == "" {
			return
		}
		volName, ok := volNameFor(src)
		if !ok || seen[volName] {
			return
		}
		seen[volName] = true
		// libvirt 卷（克隆卷等 root 属主）走 vol-delete；seed 等直接落盘文件 libvirt 不认作卷，os 兜底删文件。
		_ = ctx.Virt.DeleteVolume(pool, volName)
		if poolPath != "" {
			_ = os.Remove(filepath.Join(poolPath, volName))
		}
	}
	for _, src := range diskSources {
		tryDeleteVol(src)
	}
	tryDeleteVol(filepath.Join(poolPath, vm.Name+".qcow2"))
	reportProgress(ctx, 70, "磁盘清理完成")

	// 4. 清理 cloud-init seed 镜像（独立 seed 目录，非池卷）。
	if seedDir := taskSeedDir(); seedDir != "" {
		_ = os.Remove(filepath.Join(seedDir, vm.Name+"-seed.iso"))
	}

	// 5. 软删除数据库记录。
	if err := ctx.DB.Delete(&vm).Error; err != nil {
		return fmt.Errorf("删除虚拟机记录失败: %w", err)
	}

	setTaskResultVM(ctx, map[string]interface{}{"vm": vm.Name}, vm.ID, vm.Name)
	return nil
}

// execCloneVM 克隆虚拟机（对应 virsh vol-clone + virsh define）。
// payload：{source_id*, name*, storage_pool, vcpu, memory_mb, network}。
// 注意：克隆卷落在源系统盘所在存储池，storage_pool 仅写入 DB 记录（virt 层按源池克隆）。
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

	// 新域 UUID 与首个网卡 MAC 从 libvirt 查询（克隆后重新生成）。
	uuid := ""
	nicMAC := ""
	if ns, err := ctx.Virt.GetDomainSpec(name); err == nil && ns != nil {
		uuid = ns.UUID
		if len(ns.Interfaces) > 0 {
			nicMAC = ns.Interfaces[0].MAC
		}
	}
	if uuid == "" {
		uuid, err = randomUUID()
		if err != nil {
			return fmt.Errorf("生成虚拟机 UUID 失败: %w", err)
		}
	}

	pool := storagePool
	if pool == "" {
		pool = src.StoragePool
	}
	clone := model.VM{
		UUID:        uuid,
		Name:        name,
		HostID:      src.HostID,
		Template:    "clone",
		StoragePool: pool,
		VCPU:        source.VCPU,
		MemoryMB:    source.MemoryMB,
		DiskGB:      src.DiskGB,
		MACAddress:  nicMAC,
		Status:      "shut off",
	}
	if err := ctx.DB.Create(&clone).Error; err != nil {
		return fmt.Errorf("记录克隆虚拟机失败: %w", err)
	}
	reportProgress(ctx, 70, "克隆记录已落盘")

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

	uuid, err := randomUUID()
	if err != nil {
		return fmt.Errorf("生成虚拟机 UUID 失败: %w", err)
	}
	mac, err := randomMAC()
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
		Template:    "image",
		StoragePool: poolName, // 实际克隆卷落在镜像所在池
		VCPU:        vcpu,
		MemoryMB:    memoryMB,
		DiskGB:      int(img.SizeGB + 0.5),
		MACAddress:  mac,
		Status:      "shut off",
	}
	if err := ctx.DB.Create(&vm).Error; err != nil {
		cleanup()
		cloneCleaned = true
		return fmt.Errorf("记录虚拟机失败: %w", err)
	}
	cloneCleaned = true // 创建成功，保留克隆卷

	setTaskResultVM(ctx, map[string]interface{}{"vm_id": vm.ID}, vm.ID, vm.Name)
	return nil
}

// execStopVM 停止虚拟机：先优雅关机（对应 virsh shutdown），轮询等待其真正关闭，
// 超时后强制断电（对应 virsh destroy）。
// payload：{vm_id*}。
func execStopVM(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}

	vmID, ok := intParam(ctx.Payload, "vm_id")
	if !ok || vmID <= 0 {
		return errors.New("缺少虚拟机 ID 参数")
	}

	var vm model.VM
	if err := ctx.DB.First(&vm, uint(vmID)).Error; err != nil {
		return fmt.Errorf("虚拟机不存在: %w", err)
	}

	// 先优雅关机，轮询等待其真正关闭（最多 ~15s），超时仍运行则强制。
	shutdownErr := ctx.Virt.ShutdownDomain(vm.Name)
	if shutdownErr != nil {
		// 优雅关机调用失败（如域不存在），直接强制断电。
		if ferr := ctx.Virt.DestroyDomain(vm.Name); ferr != nil {
			return fmt.Errorf("强制停止虚拟机失败: %w", ferr)
		}
	} else {
		shutOff := false
		for i := 0; i < 15; i++ {
			time.Sleep(1 * time.Second)
			state, err := ctx.Virt.GetDomainState(vm.Name)
			if err == nil && state == "shut off" {
				shutOff = true
				break
			}
			reportProgress(ctx, 10+i*5, "等待虚拟机关闭")
		}
		if !shutOff {
			if err := ctx.Virt.DestroyDomain(vm.Name); err != nil {
				return fmt.Errorf("强制停止虚拟机失败: %w", err)
			}
		}
	}

	// 更新状态。
	if err := ctx.DB.Model(&vm).Update("status", "shut off").Error; err != nil {
		return fmt.Errorf("更新虚拟机状态失败: %w", err)
	}

	setTaskResultVM(ctx, map[string]interface{}{"vm": vm.Name}, vm.ID, vm.Name)
	return nil
}
