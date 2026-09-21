package tasks

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// execCreateVM 创建虚拟机（对应 virsh vol-create-as + virsh define）。
// payload 复刻原 CreateVM 请求体：
// {name*, host_id, storage_pool, vcpu, memory_mb, disk_gb,
//
//	disks[{create_gb,source,source_image_id,cloud_init}], interfaces[{type,source,mac,model}],
//	network, iso_path, cloud_init{hostname,user,password,ssh_key,net_mode,ip,gateway,dns}}。
//	source_image_id 云镜像走真增量盘（linked clone）：子卷 backing 指向镜像文件，VM 不直引共享镜像。
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

	storagePool, _ := strParam(payload, "storage_pool")
	network, _ := strParam(payload, "network")
	isoPath, _ := strParam(payload, "iso_path")
	machine, _ := strParam(payload, "machine")
	cpuMode, _ := strParam(payload, "cpu_mode")
	vcpu, _ := intParam(payload, "vcpu")
	memoryMB, _ := intParam(payload, "memory_mb")
	diskGB, _ := intParam(payload, "disk_gb")
	hostID, hasHostID := intParam(payload, "host_id")

	// 机器类型白名单：q35（推荐，与手工模板一致）/ pc（i440fx 兼容别名）/ 空=libvirt 自动
	switch machine {
	case "", "q35", "pc":
	default:
		return errors.New("机器类型只支持 q35 或 pc")
	}
	// CPU 模式白名单：host-passthrough（直通，缺省）/ default（显式不输出 cpu 节点，用 libvirt 缺省模型）
	switch cpuMode {
	case "", "host-passthrough", "default":
	default:
		return errors.New("CPU 模式只支持 host-passthrough（直通）或 default")
	}

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
		storagePool = DefaultStoragePoolResolver()
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
	uuid, err := virt.RandomUUID()
	if err != nil {
		return fmt.Errorf("生成虚拟机 UUID 失败: %w", err)
	}
	firstMAC, err := virt.RandomMAC()
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
		Machine:  machine,
		CPUMode:  cpuMode,
		Boot:     virt.BootSpec{Devices: []string{"hd"}},
		Graphics: virt.GraphicsSpec{Type: "vnc", Port: -1},
	}

	// 记录已建卷（pool:volName），失败时回滚清理。
	var createdVols []string
	totalDiskGB := 0
	seedPath := ""
	// 首块盘实际落地池：云镜像增量盘落在镜像所在池（CloneVolumeFromVolSized 按源卷池建卷），
	// 可能与请求的 storage_pool 不同。VM.StoragePool 必须记真实池（首盘口径，与 execCloneVM 一致），
	// 否则删除守卫按请求池前缀判「池外」，增量卷会残留成孤儿（实测踩中过同型问题）。
	firstDiskPool := ""
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

	// 逐磁盘落地：create_gb → 建卷；source → 直接引用；source_image_id → 云镜像增量盘（linked clone）。
	reportProgress(ctx, 10, "开始创建虚拟机磁盘")
	poolPath := ""
	for i, d := range disks {
		// 自定义卷名：合法性校验（与 VM 名同规则，新建卷与云镜像增量盘共用），冲突靠 libvirt 建卷报错兜底
		if d.VolName != "" && !validateVMName(d.VolName) {
			cleanup()
			return fmt.Errorf("磁盘卷名 %s 不合法（只允许字母、数字、下划线和连字符）", d.VolName)
		}
		var source string
		// 分支优先级：source_image_id（云镜像增量盘）> create_gb（新建空卷）> source（直引）。
		// 云镜像在前：磁盘项同时给 source_image_id 与 create_gb 时，create_gb 只作为子卷容量，
		// 决不能退化成新建空盘——云镜像方式的核心语义是「以镜像为模板的增量盘」。
		switch {
		case d.SourceImageID > 0:
			var img model.Image
			if err := ctx.DB.First(&img, d.SourceImageID).Error; err != nil {
				cleanup()
				return fmt.Errorf("云镜像不存在: %w", err)
			}
			// 云镜像走真增量盘（linked clone，对应 qemu-img create -f qcow2 -b <镜像>）：建
			// <vm名>[-dN] 子卷、backing file 指向镜像文件，本机只写差异。
			// 原先直接引用共享镜像文件，实害（实测）：同基镜像的其他 VM 运行时本机开机
			// 报 qemu 写锁冲突 500；快照会对基镜像本体 qemu-img snapshot 污染所有兄弟机。
			srcPool, srcVol, lerr := ctx.Virt.LookupVolByPath(img.Path)
			if lerr != nil {
				// 镜像不在任何 libvirt 池内（历史登记的池外文件）无法做增量克隆，
				// 回退旧行为：直接引用镜像文件（与镜像共用，镜像删除前须先删本机）。
				log.Printf("[tasks] 云镜像不在任何存储池内，回退为直接引用（不可增量） vm=%s image=%s err=%v", name, img.Path, lerr)
				source = img.Path
			} else {
				// 实际容量：create_gb 指定则用之；未指定沿用源卷虚拟容量；都取不到回退 20（与默认盘一致）。
				// 指定容量小于源卷时按源卷钳制——qcow2 虚拟容量小于 backing 会截断镜像已装内容。
				srcGB, gerr := ctx.Virt.VolumeCapacityGB(srcPool, srcVol)
				if gerr != nil {
					// 取不到只影响容量登记与展示，不阻断建机
					log.Printf("[tasks] 获取云镜像卷容量失败 vm=%s pool=%s vol=%s err=%v", name, srcPool, srcVol, gerr)
				}
				capGB := d.CreateGB
				if capGB <= 0 || (srcGB > 0 && capGB < srcGB) {
					capGB = srcGB
				}
				if capGB <= 0 {
					capGB = 20
				}
				volName := diskVolumeName(name, i, d.VolName)
				newPath, cerr := ctx.Virt.CloneVolumeFromVolSized(srcPool, srcVol, volName, capGB)
				if cerr != nil {
					cleanup()
					return fmt.Errorf("创建增量系统盘失败: %w", cerr)
				}
				// 失败清理同建卷口径：cleanup 按「池:卷名.qcow2」回滚，define 失败时增量卷可回收
				createdVols = append(createdVols, srcPool+":"+volName+".qcow2")
				source = newPath
				totalDiskGB += capGB
				if firstDiskPool == "" {
					firstDiskPool = srcPool
				}
			}
		case d.CreateGB > 0:
			volName := diskVolumeName(name, i, d.VolName)
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
			if firstDiskPool == "" {
				firstDiskPool = storagePool
			}
		case d.Source != "":
			source = d.Source
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
				m, err := virt.RandomMAC()
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

	// DB 记首块盘的实际落地池（无盘落池时回退请求池）
	dbPool := storagePool
	if firstDiskPool != "" {
		dbPool = firstDiskPool
	}
	vm := model.VM{
		UUID:        uuid,
		Name:        name,
		HostID:      host.ID,
		StoragePool: dbPool,
		VCPU:        vcpu,
		MemoryMB:    memoryMB,
		DiskGB:      totalDiskGB,
		MACAddress:  nicMAC,
		Status:      model.VMStatusShutOff,
	}
	if err := ctx.DB.Create(&vm).Error; err != nil {
		cleanup()
		return fmt.Errorf("创建虚拟机记录失败: %w", err)
	}
	reportProgress(ctx, 70, "虚拟机记录已落盘，正在定义域")
	if err := ctx.Virt.DefineDomain(xmlstr); err != nil {
		if delErr := ctx.DB.Delete(&vm).Error; delErr != nil {
			// 回滚失败会留下"DB 有记录、libvirt 无域"的孤儿记录（用户可见且无法启动），必须留痕
			log.Printf("[tasks] 定义失败回滚 VM 记录失败 vm=%s err=%v", vm.Name, delErr)
		}
		cleanup()
		return fmt.Errorf("定义虚拟机失败: %w", err)
	}

	// 创建者自动获得该 VM 的授权（借鉴堡垒机 4A：谁创建谁可用；admin 创建对 admin 无感——admin 不走授权判定）
	// 授权失败只记日志不阻断建机（VM 已定义成功，回滚代价大于补授权），但必须留痕否则学生看不到自己的机器且无从排查
	if ctx.Task.UserID != nil {
		if err := ctx.DB.Create(&model.VMGrant{UserID: *ctx.Task.UserID, VMID: vm.ID, GrantedBy: *ctx.Task.UserID}).Error; err != nil {
			log.Printf("[tasks] 创建者自动授权失败 vm=%s user=%d err=%v", vm.Name, *ctx.Task.UserID, err)
		}
	}

	setTaskResultVM(ctx, map[string]interface{}{"vm_id": vm.ID, "disk_gb": totalDiskGB}, vm.ID, vm.Name)
	return nil
}

// diskVolumeName 第 i 块磁盘的卷名（不含扩展名）：自定义名优先；否则首盘 <vm名>、后续 <vm名>-dN（N 从 2 起）。
// 新建卷与云镜像增量盘共用同一命名，删除兜底（execDeleteVM）与回收站恢复重建均按该约定找卷。
func diskVolumeName(name string, i int, custom string) string {
	if custom != "" {
		return custom
	}
	if i > 0 {
		return fmt.Sprintf("%s-d%d", name, i+1)
	}
	return name
}

// createDiskReq 创建 VM 时的磁盘描述。
// 分支优先级：source_image_id（云镜像增量盘，create_gb 可选作子卷容量）> create_gb（新建卷）
// > source（直接引用现有卷/镜像路径）。
type createDiskReq struct {
	CreateGB      int
	Source        string
	SourceImageID uint
	CloudInit     *virt.CloudInitSpec
	VolName       string // 自定义卷名（可空=自动命名 <vm名>[-dN]）
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
		if s, ok := strParam(m, "vol_name"); ok {
			d.VolName = s
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
