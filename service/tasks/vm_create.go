// 本文件是 create_vm 任务的主流程编排：解析参数与默认值、逐盘落地（新建卷/直引/云镜像增量盘）、
// 挂 ISO 或网络安装源、生成 cloud-init seed、组装 DomainSpec 并 define，最后落库与可选初始化。
// 拆出的辅助职责见同包 vm_create_parse.go（payload 解析）、vm_create_install.go（安装源下载）、
// vm_create_provision.go（建机后初始化与凭据托管）。
package tasks

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/ansible"
	"github.com/jiuzhao/vmops/service/virt"
)

// execCreateVM 创建虚拟机（对应 virsh vol-create-as + virsh define）。
// payload 复刻原 CreateVM 请求体：
// {name*, host_id, storage_pool, vcpu, memory_mb, disk_gb,
//
//	disks[{create_gb,source,source_image_id,cloud_init}], interfaces[{type,source,mac,model}],
//	network, iso_path, cloud_init{hostname,user,password,ssh_key,net_mode,ip,gateway,dns},
//	provision{start,wait_ip,ssh_user,password_enc,salt}}。
//	source_image_id 云镜像走真增量盘（linked clone）：子卷 backing 指向镜像文件，VM 不直引共享镜像。
//	provision 为可选「建机后初始化」块：定义成功后自动开机 → 等 DHCP 租约回填 IP → 托管凭据
//	（向导「自动初始化」与设计器落地共用此实现）；缺省不带则行为与历史一致（只 define）。
func execCreateVM(ctx *ExecContext, masterSecret string) error {
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
	// 网络安装源（FTP/HTTP 直接内核引导，等价 virt-install --location）
	installURL, _ := strParam(payload, "install_url")
	installKernel, _ := strParam(payload, "install_kernel")
	installInitrd, _ := strParam(payload, "install_initrd")
	installCmdline, _ := strParam(payload, "install_cmdline")
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

	// 网络安装源（FTP/HTTP）：下载内核与 initrd 到本地，直接内核引导（等价 virt-install --location）。
	// 与 iso_path 互斥：给了安装源就不再挂 ISO 光驱。
	if installURL != "" {
		reportProgress(ctx, 32, "下载安装源内核与 initrd…")
		kp, ip, kerr := fetchInstallKernel(ctx, name, installURL, installKernel, installInitrd)
		if kerr != nil {
			cleanup()
			return kerr
		}
		spec.Kernel = kp
		spec.Initrd = ip
		cmdline := installCmdline
		if cmdline == "" {
			cmdline = "inst.repo=" + installURL // RHEL/Rocky 系；Debian 系可用 install_cmdline 覆盖为 url=<url>
		}
		spec.Cmdline = cmdline + " console=ttyS0,115200n8"
		spec.Boot.Devices = []string{"hd"}
		reportProgress(ctx, 38, "安装源就绪（直接内核引导）")
	} else if isoPath != "" {
		// 兼容旧 iso_path：挂只读 cdrom 安装盘，引导优先光驱。
		spec.Disks = append(spec.Disks, virt.DiskSpec{
			Type: "file", Device: "cdrom", Driver: "raw", Bus: "ide",
			Source: isoPath, ReadOnly: true,
			Target: virt.NextDiskTarget(spec, "ide"),
		})
		spec.Boot.Devices = []string{"cdrom", "hd"}
	}

	// cloud-init：顶层缺失时从磁盘项中查找，生成 seed ISO 落到 seed 目录，挂为只读 cdrom。
	cfg := cloudInit
	keyInjected := false
	ansibleUser := "root" // 平台公钥实际落在哪个用户家目录（见 model.VM.AnsibleUser 注释）
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
		// 平台 ansible 公钥注入（P4-S4 免密渐进）：cloud-init 建机直通——此后该 VM
		// 走私钥免密通道，ansible inventory 不再瞬时承载口令文件。密钥不可用只降级
		// 回口令通道（warn），不阻断建机
		if _, pub, kerr := ansible.EnsureKeyPair(config.DataPath("ansible")); kerr == nil {
			cfg.SSHKeys = append(cfg.SSHKeys, pub)
			keyInjected = true
			// 公钥写进的是 cloud-init 配的那个用户家目录，ansible 免密必须用它登录
			if u := strings.TrimSpace(cfg.User); u != "" {
				ansibleUser = u
			}
		} else {
			log.Printf("[tasks] 警告: 平台密钥不可用，VM %s 降级口令通道: %v", name, kerr)
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
		UUID:         uuid,
		Name:         name,
		HostID:       host.ID,
		StoragePool:  dbPool,
		VCPU:         vcpu,
		MemoryMB:     memoryMB,
		DiskGB:       totalDiskGB,
		MACAddress:   nicMAC,
		Status:       model.VMStatusShutOff,
		AnsibleReady: keyInjected,
		AnsibleUser:  ansibleUser,
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

	// 可选「建机后初始化」：开机 → 等 IP → 托管凭据（向导「自动初始化」/ 设计器落地共用同一实现）。
	// 失败不回滚（VM 已定义成功，回滚代价大于收益），仅置任务失败并留痕；vm_id 已在结果里，调用方仍能定位。
	if prov := parseTaskProvision(payload); prov != nil {
		if err := runProvision(ctx, &vm, prov, masterSecret); err != nil {
			return fmt.Errorf("虚拟机已创建但初始化失败: %w", err)
		}
	}
	return nil
}
