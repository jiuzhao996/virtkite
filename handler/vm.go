package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// vmNameRegex 虚拟机名称只允许字母、数字、下划线和连字符
var vmNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateVMName 校验虚拟机名称合法性
func validateVMName(name string) bool {
	return vmNameRegex.MatchString(name)
}

// randomMAC 生成一个 52:54:00:xx:xx:xx 格式的 KVM 默认 MAC 地址
func randomMAC() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[0], b[1], b[2]), nil
}

// randomUUID 生成一个符合 RFC 4122 的 v4 UUID 字符串
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

// VMHandler 虚拟机处理器
type VMHandler struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewVMHandler 创建虚拟机处理器
func NewVMHandler(db *gorm.DB) *VMHandler {
	return &VMHandler{DB: db, Virt: virt.New()}
}

// VMInfo virsh返回的虚拟机信息
type VMInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// HostInfo 宿主机信息
type HostInfo struct {
	Hostname string `json:"hostname"`
	Kernel   string `json:"kernel"`
	CPUs     string `json:"cpus"`
	MemTotal string `json:"memTotal"`
	MemUsed  string `json:"memUsed"`
	Uptime   string `json:"uptime"`
}

// ListVMs 获取虚拟机列表
func (h *VMHandler) ListVMs(c *gin.Context) {
	// 从数据库查询虚拟机
	var vms []model.VM
	if err := h.DB.Preload("Host").Find(&vms).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询虚拟机失败", err)
		return
	}

	// 同步 libvirt 状态：一次 RPC 拉取所有域状态，避免逐个查询
	stateMap, err := h.Virt.GetAllDomainStates()
	if err == nil && stateMap != nil {
		for i := range vms {
			if s, ok := stateMap[vms[i].Name]; ok {
				vms[i].Status = s
				h.DB.Model(&vms[i]).Update("status", s)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data": gin.H{
			"total": len(vms),
			"items": vms,
		},
	})
}

// GetVM 获取虚拟机详情
func (h *VMHandler) GetVM(c *gin.Context) {
	id := c.Param("id")

	var vm model.VM
	if err := h.DB.Preload("Host").First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	// 同步 libvirt 状态
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != "" {
		vm.Status = state
		h.DB.Model(&vm).Update("status", state)
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data":    vm,
	})
}

// GetVMDetail 返回 VM 详情（基础信息 + 磁盘/网卡 + 运行使用率），供详情页展示。
func (h *VMHandler) GetVMDetail(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.Preload("Host").First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	// 同步 libvirt 状态
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != "" {
		vm.Status = state
		h.DB.Model(&vm).Update("status", state)
	}

	// 磁盘/网卡（XML 解析，停机亦可读）
	disks, nics, err := h.Virt.ListDomainDevices(vm.Name)
	if err != nil {
		disks, nics = []virt.Device{}, []virt.Device{}
	}

	// 运行使用率（仅 running 有意义；CPU% 由前端按 cputime 差值计算）
	usage := gin.H{"running": false, "mem_used_mb": 0, "mem_total_mb": 0, "guest_used_mb": 0, "guest_total_mb": 0, "vcpus": 0, "cpu_time_ns": 0, "host_cpus": 0}
	if vm.Status == "running" {
		if info, err := h.Virt.GetDomainInfo(vm.Name); err == nil {
			hostCpus := 0
			if out, cerr := exec.Command("nproc").Output(); cerr == nil {
				_, _ = fmt.Sscanf(string(out), "%d", &hostCpus)
			}
			// 客户机真实内存（balloon memory_stats，KvmDash 同款口径）；无 balloon 时为 0，前端回退分配口径
			guestUsed, guestTotal := 0, 0
			if ms := h.Virt.GetMemoryStats(vm.Name); len(ms) > 0 {
				if actual, ok := ms["actual"]; ok && actual > 0 {
					guestTotal = int(actual / 1024)
					if unused, ok := ms["unused"]; ok && unused <= actual {
						guestUsed = int((actual - unused) / 1024)
					} else if rss, ok := ms["rss"]; ok {
						guestUsed = int(rss / 1024)
					}
				}
			}
			usage = gin.H{
				"running":        true,
				"mem_used_mb":    int(info.MemKiB / 1024),
				"mem_total_mb":   int(info.MaxMemKiB / 1024),
				"guest_used_mb":  guestUsed,
				"guest_total_mb": guestTotal,
				"vcpus":          info.VCPUs,
				"cpu_time_ns":    info.CPUTimeNS,
				"host_cpus":      hostCpus,
			}
		}
	}

	Success(c, gin.H{
		"vm":    vm,
		"disks": disks,
		"nics":  nics,
		"usage": usage,
	})
}

// createDiskReq 创建 VM 时的磁盘描述：三选一
// (1) create_gb 新建卷；(2) source 直接引用现有卷/镜像路径；(3) source_image_id 引用云镜像（DB images.id）。
type createDiskReq struct {
	CreateGB      int                 `json:"create_gb"`       // 新建卷容量（GB）
	Source        string              `json:"source"`          // 直接引用现有卷/镜像路径
	SourceImageID uint                `json:"source_image_id"` // 引用云镜像，直接引用不拷贝
	CloudInit     *virt.CloudInitSpec `json:"cloud_init,omitempty"`
}

// CreateVM 创建虚拟机（向导/克隆模板入口，对应 virsh vol-create-as + virsh define）。
// 磁盘支持三种来源；cloud_init 非空时生成 seed ISO 并挂为只读 cdrom；可选 iso_path 挂安装光驱。
// 镜像引用方式：source_image_id 直接引用云镜像文件（VM 与镜像共用文件，删除镜像前需先删引用 VM）。
func (h *VMHandler) CreateVM(c *gin.Context) {
	var req struct {
		Name        string               `json:"name" binding:"required"`
		HostID      uint                 `json:"host_id"`
		Template    string               `json:"template"`
		StoragePool string               `json:"storage_pool"`
		VCPU        int                  `json:"vcpu"`
		MemoryMB    int                  `json:"memory_mb"`
		DiskGB      int                  `json:"disk_gb"` // 旧字段：未提供 disks 时默认建盘容量
		Disks       []createDiskReq      `json:"disks"`
		Interfaces  []virt.InterfaceSpec `json:"interfaces"`
		Network     string               `json:"network"`  // 便捷字段：未提供 interfaces 时使用
		ISOPath     string               `json:"iso_path"` // 兼容旧字段：生成 cdrom 安装盘
		CloudInit   *virt.CloudInitSpec  `json:"cloud_init,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	// 校验名称合法性
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "虚拟机名称只允许字母、数字、下划线和连字符")
		return
	}

	// 查宿主机：未指定时取平台登记的首台
	var host model.Host
	if req.HostID != 0 {
		if err := h.DB.First(&host, req.HostID).Error; err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "宿主机不存在", err)
			return
		}
	} else {
		hst, err := h.firstHost()
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "请先在宿主机管理中登记宿主机", err)
			return
		}
		host = *hst
	}

	// 默认值
	if req.StoragePool == "" {
		req.StoragePool = "vmops"
	}
	if req.VCPU == 0 {
		req.VCPU = 1
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = 1024
	}
	if req.Network == "" && len(req.Interfaces) == 0 {
		req.Network = "default"
	}
	// 旧调用兼容：未提供 disks 时按 disk_gb 建默认盘
	if len(req.Disks) == 0 {
		gb := req.DiskGB
		if gb == 0 {
			gb = 20
		}
		req.Disks = []createDiskReq{{CreateGB: gb}}
	}

	// 生成 UUID 与首个网卡 MAC
	uuid, err := randomUUID()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "生成虚拟机 UUID 失败", err)
		return
	}
	firstMAC, err := randomMAC()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "生成虚拟机 MAC 失败", err)
		return
	}

	// 组装 DomainSpec
	spec := &virt.DomainSpec{
		Name:     req.Name,
		UUID:     uuid,
		VCPU:     req.VCPU,
		MemoryMB: req.MemoryMB,
		OSType:   "hvm",
		Arch:     "x86_64",
		Boot:     virt.BootSpec{Devices: []string{"hd"}},
		Graphics: virt.GraphicsSpec{Type: "vnc", Port: -1},
	}

	// 记录已建卷（pool:volName），失败时回滚清理
	var createdVols []string
	diskGB := 0
	seedPath := ""
	cleanup := func() {
		for _, cv := range createdVols {
			parts := strings.SplitN(cv, ":", 2)
			if len(parts) == 2 {
				_ = h.Virt.DeleteVolume(parts[0], parts[1])
			}
		}
		if seedPath != "" {
			_ = os.Remove(seedPath)
		}
	}

	// 逐磁盘落地：create_gb → 建卷；source → 直接引用；source_image_id → 引用云镜像文件
	poolPath := ""
	for i, d := range req.Disks {
		var source string
		switch {
		case d.CreateGB > 0:
			volName := req.Name
			if i > 0 {
				volName = fmt.Sprintf("%s-d%d", req.Name, i+1)
			}
			if _, err := h.Virt.CreateVolume(req.StoragePool, volName, d.CreateGB); err != nil {
				cleanup()
				ErrorResponse(c, http.StatusInternalServerError, err)
				return
			}
			createdVols = append(createdVols, req.StoragePool+":"+volName+".qcow2")
			if poolPath == "" {
				if poolPath, err = h.Virt.GetPoolPath(req.StoragePool); err != nil {
					cleanup()
					ErrorResponse(c, http.StatusInternalServerError, err)
					return
				}
			}
			source = filepath.Join(poolPath, volName+".qcow2")
			diskGB += d.CreateGB
		case d.Source != "":
			source = d.Source
		case d.SourceImageID > 0:
			var img model.Image
			if err := h.DB.First(&img, d.SourceImageID).Error; err != nil {
				cleanup()
				ErrorWithMessage(c, http.StatusBadRequest, "云镜像不存在", err)
				return
			}
			// 云镜像直接引用，不拷贝：VM 与镜像共用文件，镜像删除前需先删引用 VM
			source = img.Path
		default:
			cleanup()
			Fail(c, http.StatusBadRequest, "磁盘参数不完整（create_gb / source / source_image_id 三选一）")
			return
		}
		spec.Disks = append(spec.Disks, virt.DiskSpec{
			Type:   "file",
			Device: "disk",
			Driver: "qcow2",
			Bus:    "virtio",
			Source: source,
			Target: virt.NextDiskTarget(spec, "virtio"),
		})
	}

	// 兼容旧 iso_path：挂只读 cdrom 安装盘，引导优先光驱
	if req.ISOPath != "" {
		spec.Disks = append(spec.Disks, virt.DiskSpec{
			Type: "file", Device: "cdrom", Driver: "raw", Bus: "ide",
			Source: req.ISOPath, ReadOnly: true,
			Target: virt.NextDiskTarget(spec, "ide"),
		})
		spec.Boot.Devices = []string{"cdrom", "hd"}
	}

	// cloud-init：生成 seed ISO 落到存储池路径，挂为只读 cdrom
	cfg := req.CloudInit
	if cfg == nil {
		for i := range req.Disks {
			if req.Disks[i].CloudInit != nil {
				cfg = req.Disks[i].CloudInit
				break
			}
		}
	}
	if cfg != nil {
		if cfg.Hostname == "" {
			cfg.Hostname = req.Name
		}
		seedBytes, err := virt.GenerateSeedISO(cfg)
		if err != nil {
			cleanup()
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		// seed 写到独立 seed 目录（web 可写、qemu 可读），避免依赖存储池目录权限
		seedDir := config.GlobalConfig.SeedDir
		if seedDir == "" {
			seedDir = "/home/jiuzhao/vmops/data/seed"
		}
		if err := os.MkdirAll(seedDir, 0755); err != nil {
			cleanup()
			ErrorWithMessage(c, http.StatusInternalServerError, "创建 cloud-init seed 目录失败", err)
			return
		}
		seedPath = filepath.Join(seedDir, req.Name+"-seed.iso")
		if err := os.WriteFile(seedPath, seedBytes, 0644); err != nil {
			cleanup()
			ErrorWithMessage(c, http.StatusInternalServerError, "写入 cloud-init seed 镜像失败", err)
			return
		}
		spec.Disks = append(spec.Disks, virt.DiskSpec{
			Type: "file", Device: "cdrom", Driver: "raw", Bus: "ide",
			Source: seedPath, ReadOnly: true,
			Target: virt.NextDiskTarget(spec, "ide"),
		})
		spec.Boot.Devices = []string{"cdrom", "hd"}
	}

	// 网卡：未显式提供 interfaces 时按 network 便捷字段生成
	nicMAC := firstMAC
	if len(req.Interfaces) > 0 {
		for i := range req.Interfaces {
			if req.Interfaces[i].Type == "" {
				req.Interfaces[i].Type = "network"
			}
			if req.Interfaces[i].Source == "" {
				req.Interfaces[i].Source = req.Network
				if req.Interfaces[i].Source == "" {
					req.Interfaces[i].Source = "default"
				}
			}
			if req.Interfaces[i].MAC == "" {
				m, err := randomMAC()
				if err != nil {
					cleanup()
					ErrorWithMessage(c, http.StatusInternalServerError, "生成网卡 MAC 失败", err)
					return
				}
				req.Interfaces[i].MAC = m
			}
			if req.Interfaces[i].Model == "" {
				req.Interfaces[i].Model = "virtio"
			}
			if i == 0 {
				nicMAC = req.Interfaces[i].MAC
			}
			spec.Interfaces = append(spec.Interfaces, req.Interfaces[i])
		}
	} else {
		spec.Interfaces = append(spec.Interfaces, virt.InterfaceSpec{
			Type: "network", Source: req.Network, MAC: firstMAC, Model: "virtio",
		})
	}

	// BuildDomainXML 生成完整定义（纯函数），再写 DB 记录与 define
	xmlstr, err := virt.BuildDomainXML(spec)
	if err != nil {
		cleanup()
		ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
		return
	}

	vm := model.VM{
		UUID:        uuid,
		Name:        req.Name,
		HostID:      host.ID,
		Template:    req.Template,
		StoragePool: req.StoragePool,
		VCPU:        req.VCPU,
		MemoryMB:    req.MemoryMB,
		DiskGB:      diskGB,
		MACAddress:  nicMAC,
		Status:      "shut off",
	}
	if err := h.DB.Create(&vm).Error; err != nil {
		cleanup()
		ErrorWithMessage(c, http.StatusInternalServerError, "创建虚拟机失败", err)
		return
	}
	if err := h.Virt.DefineDomain(xmlstr); err != nil {
		h.DB.Delete(&vm)
		cleanup()
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, vm)
}

// GetVMSpec 返回虚拟机完整配置（DB 记录 + DomainSpec，spec 含 raw_xml 回显）。
func (h *VMHandler) GetVMSpec(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.Preload("Host").First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	// 同步 libvirt 状态
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != "" {
		vm.Status = state
		h.DB.Model(&vm).Update("status", state)
	}

	spec, err := h.Virt.GetDomainSpec(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm, "spec": spec})
}

// UpdateVMSpec 整体重 define 虚拟机配置（对应 virsh edit 后 define）。
// 请求体为完整 DomainSpec（raw_xml 忽略）；VM 运行中禁止修改，须先关机。
// 同步回写 DB 的 vcpu / memory_mb / disk_gb（首个磁盘容量近似）/ mac_address（首个网卡）。
func (h *VMHandler) UpdateVMSpec(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var spec virt.DomainSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	// 运行时禁止整体重定义，提示先关机
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state == "running" {
		Fail(c, http.StatusBadRequest, "虚拟机运行中，请先关机后再修改配置")
		return
	}

	// 名称/UUID 以 DB 为准，防止定义错位；raw_xml 由 BuildDomainXML 重建，忽略回显原文
	spec.Name = vm.Name
	if spec.UUID == "" {
		spec.UUID = vm.UUID
	}
	spec.CloudInit = nil
	spec.RawXML = ""

	xmlstr, err := virt.BuildDomainXML(&spec)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
		return
	}
	if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 同步 DB 摘要字段
	updates := map[string]interface{}{"vcpu": spec.VCPU, "memory_mb": spec.MemoryMB}
	if len(spec.Disks) > 0 {
		if gb := h.Virt.DiskSizeGB(spec.Disks[0].Source); gb > 0 {
			updates["disk_gb"] = gb
		}
	}
	if len(spec.Interfaces) > 0 && spec.Interfaces[0].MAC != "" {
		updates["mac_address"] = spec.Interfaces[0].MAC
	}
	h.DB.Model(&vm).Updates(updates)

	Success(c, gin.H{"vm": vm.Name, "message": "配置已更新"})
}

// PauseVM 暂停虚拟机（对应 virsh suspend）。
func (h *VMHandler) PauseVM(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if err := h.Virt.PauseDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	h.DB.Model(&vm).Update("status", "paused")
	Success(c, gin.H{"vm": vm.Name, "message": "虚拟机已暂停"})
}

// ResumeVM 恢复已暂停的虚拟机（对应 virsh resume）。
func (h *VMHandler) ResumeVM(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if err := h.Virt.ResumeDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	h.DB.Model(&vm).Update("status", "running")
	Success(c, gin.H{"vm": vm.Name, "message": "虚拟机已恢复"})
}

// AttachDisk 热插拔磁盘（对应 virsh attach-device，运行中生效并落配置）。
// body: {disk: DiskSpec}；disk.Target 为空时按 bus 自动分配（NextDiskTarget）。
func (h *VMHandler) AttachDisk(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Disk virt.DiskSpec `json:"disk"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Disk.Source == "" {
		ErrorWithMessage(c, http.StatusBadRequest, "磁盘参数错误（需提供 source）", err)
		return
	}
	if req.Disk.Bus == "" {
		req.Disk.Bus = "virtio"
	}
	if req.Disk.Device == "" {
		req.Disk.Device = "disk"
	}
	if req.Disk.Target == "" {
		spec, err := h.Virt.GetDomainSpec(vm.Name)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		req.Disk.Target = virt.NextDiskTarget(spec, req.Disk.Bus)
	}

	if err := h.Virt.AttachDisk(vm.Name, req.Disk); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "disk": req.Disk})
}

// DetachDisk 移除磁盘（对应 virsh detach-device，按 target dev 匹配）。
func (h *VMHandler) DetachDisk(c *gin.Context) {
	id := c.Param("id")
	target := c.Param("target")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if err := h.Virt.DetachDisk(vm.Name, target); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "target": target})
}

// AttachInterface 添加网卡（对应 virsh attach-interface，运行中生效并落配置）。
// body: {interface: InterfaceSpec}；mac 为空自动生成。
func (h *VMHandler) AttachInterface(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Interface virt.InterfaceSpec `json:"interface"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if req.Interface.Type == "" {
		req.Interface.Type = "network"
	}
	if req.Interface.Source == "" {
		req.Interface.Source = "default"
	}
	if req.Interface.MAC == "" {
		mac, err := randomMAC()
		if err != nil {
			ErrorWithMessage(c, http.StatusInternalServerError, "生成网卡 MAC 失败", err)
			return
		}
		req.Interface.MAC = mac
	}

	if err := h.Virt.AttachInterface(vm.Name, req.Interface); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "interface": req.Interface})
}

// DetachInterface 移除网卡（对应 virsh detach-interface，按 MAC 地址匹配）。
func (h *VMHandler) DetachInterface(c *gin.Context) {
	id := c.Param("id")
	mac := c.Param("mac")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if err := h.Virt.DetachInterface(vm.Name, mac); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "mac": mac})
}

// SetVcpu 调整 CPU 核数（对应 virsh setvcpus），同步 DB。
// 停机态通过重 define 修改持久配置（setvcpus CONFIG 无法超 <vcpu> 上限）；
// 运行态走 live API（仅可调至启动时最大核数以内，超出提示关机）。
func (h *VMHandler) SetVcpu(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		VCPU int `json:"vcpu"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.VCPU <= 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "vCPU 数量必须大于 0", err)
		return
	}

	// 停机态：读取 spec → 改 vcpu → 重建 XML → 重 define
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != "running" {
		spec, err := h.Virt.GetDomainSpec(vm.Name)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		spec.VCPU = req.VCPU
		spec.RawXML = ""
		xmlstr, err := virt.BuildDomainXML(spec)
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
			return
		}
		if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	} else {
		// 运行态：live+config 热调（超出启动时最大核数由 libvirt 报错，翻译提示）
		if err := h.Virt.SetVcpus(vm.Name, req.VCPU); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	}
	h.DB.Model(&vm).Update("vcpu", req.VCPU)
	Success(c, gin.H{"vm": vm.Name, "vcpu": req.VCPU})
}

// SetMemory 调整内存（对应 virsh setmem），同步 DB。
// 停机态通过重 define 修改持久配置；运行态走 live API（仅可调至启动时最大内存以内）。
func (h *VMHandler) SetMemory(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		MemoryMB int `json:"memory_mb"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.MemoryMB <= 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "内存大小必须大于 0", err)
		return
	}

	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != "running" {
		spec, err := h.Virt.GetDomainSpec(vm.Name)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		spec.MemoryMB = req.MemoryMB
		spec.RawXML = ""
		xmlstr, err := virt.BuildDomainXML(spec)
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
			return
		}
		if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	} else {
		if err := h.Virt.SetMemory(vm.Name, req.MemoryMB); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	}
	h.DB.Model(&vm).Update("memory_mb", req.MemoryMB)
	Success(c, gin.H{"vm": vm.Name, "memory_mb": req.MemoryMB})
}

// SetAutostart 设置开机自启（对应 virsh autostart）。
func (h *VMHandler) SetAutostart(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if err := h.Virt.SetAutostart(vm.Name, req.Enabled); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "autostart": req.Enabled})
}

// SetBoot 修改引导顺序（body {devices: []}，如 ["cdrom","hd"]）。停机状态下整体重 define。
func (h *VMHandler) SetBoot(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Devices []string `json:"devices"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Devices) == 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "引导设备列表不能为空", err)
		return
	}

	spec, err := h.Virt.GetDomainSpec(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	spec.Boot.Devices = req.Devices
	spec.RawXML = ""

	xmlstr, err := virt.BuildDomainXML(spec)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "引导配置不合法", err)
		return
	}
	if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "devices": req.Devices})
}

// GetVMStats 返回虚拟机实时性能统计（服务端差分计算 CPU/IO 速率）。
func (h *VMHandler) GetVMStats(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	stats, err := h.Virt.GetDomainStats(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, stats)
}

// CloneVM 克隆虚拟机（对应 virsh vol-clone + virsh define）。
// body: {name, storage_pool?, vcpu?, memory_mb?, network?}；系统盘 linked clone（父盘保留）。
// 注意：克隆卷落在源系统盘所在存储池，storage_pool 仅写入 DB 记录（virt 层按源池克隆）。
func (h *VMHandler) CloneVM(c *gin.Context) {
	id := c.Param("id")
	var src model.VM
	if err := h.DB.Preload("Host").First(&src, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Name        string `json:"name" binding:"required"`
		StoragePool string `json:"storage_pool"`
		VCPU        int    `json:"vcpu"`
		MemoryMB    int    `json:"memory_mb"`
		Network     string `json:"network"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "虚拟机名称只允许字母、数字、下划线和连字符")
		return
	}

	// 源 spec：可按需覆盖 vcpu/memory_mb/network（network 替换首个网卡 source）
	source, err := h.Virt.GetDomainSpec(src.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if req.VCPU > 0 {
		source.VCPU = req.VCPU
	}
	if req.MemoryMB > 0 {
		source.MemoryMB = req.MemoryMB
	}
	if req.Network != "" && len(source.Interfaces) > 0 {
		source.Interfaces[0].Source = req.Network
		source.Interfaces[0].Type = "network"
	}

	if _, err := h.Virt.CloneVMFromSpec(source, req.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 新域 UUID 与首个网卡 MAC 从 libvirt 查询（克隆后重新生成）
	uuid := ""
	nicMAC := ""
	if ns, err := h.Virt.GetDomainSpec(req.Name); err == nil {
		uuid = ns.UUID
		if len(ns.Interfaces) > 0 {
			nicMAC = ns.Interfaces[0].MAC
		}
	}
	if uuid == "" {
		uuid, _ = randomUUID()
	}

	pool := req.StoragePool
	if pool == "" {
		pool = src.StoragePool
	}
	clone := model.VM{
		UUID:        uuid,
		Name:        req.Name,
		HostID:      src.HostID,
		Template:    "clone",
		StoragePool: pool,
		VCPU:        source.VCPU,
		MemoryMB:    source.MemoryMB,
		DiskGB:      src.DiskGB,
		MACAddress:  nicMAC,
		Status:      "shut off",
	}
	if err := h.DB.Create(&clone).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "记录克隆虚拟机失败", err)
		return
	}

	Success(c, gin.H{"vm": clone.Name, "id": clone.ID})
}

// StartVM 启动虚拟机
func (h *VMHandler) StartVM(c *gin.Context) {
	id := c.Param("id")

	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	// 调用 libvirt 启动
	if err := h.Virt.StartDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 更新状态
	h.DB.Model(&vm).Update("status", "running")

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已启动",
	})
}

// StopVM 停止虚拟机
func (h *VMHandler) StopVM(c *gin.Context) {
	id := c.Param("id")

	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	// 调用 libvirt 关机：先优雅关机，轮询等待其真正关闭，超时后强制断电。
	shutdownErr := h.Virt.ShutdownDomain(vm.Name)
	if shutdownErr != nil {
		// 优雅关机调用失败（如域不存在），直接强制
		if ferr := h.Virt.DestroyDomain(vm.Name); ferr != nil {
			ErrorResponse(c, http.StatusInternalServerError, ferr)
			return
		}
	} else {
		// 轮询等待优雅关机生效（最多 ~15s），超时仍运行则强制
		shutOff := false
		for i := 0; i < 15; i++ {
			time.Sleep(1 * time.Second)
			state, err := h.Virt.GetDomainState(vm.Name)
			if err == nil && state == "shut off" {
				shutOff = true
				break
			}
		}
		if !shutOff {
			_ = h.Virt.DestroyDomain(vm.Name)
		}
	}

	// 更新状态
	h.DB.Model(&vm).Update("status", "shut off")

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已停止",
	})
}

// RestartVM 重启虚拟机
func (h *VMHandler) RestartVM(c *gin.Context) {
	id := c.Param("id")

	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	// 调用 libvirt 重启
	if err := h.Virt.RebootDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已重启",
	})
}

// DeleteVM 删除虚拟机
func (h *VMHandler) DeleteVM(c *gin.Context) {
	id := c.Param("id")

	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	// 1. 先取完整磁盘清单（含多盘/克隆卷/seed 盘），再删除域定义
	//    （spec 解析失败不阻断删除，域仍按既有流程清理）
	var diskSources []string
	if spec, err := h.Virt.GetDomainSpec(vm.Name); err == nil {
		for _, d := range spec.Disks {
			if d.Source != "" {
				diskSources = append(diskSources, d.Source)
			}
		}
	}

	// 2. 调用 libvirt 删除域定义
	if err := h.Virt.UndefineDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 3. 删除存储卷（对应 virsh vol-delete）：枚举的磁盘源 + 默认系统盘兜底。
	//    仅删除位于平台托管池路径下的卷，避免误删共享基镜像（如克隆子卷的父盘）。
	pool := vm.StoragePool
	if pool == "" {
		pool = "vmops"
	}
	// 池路径前缀（用于判定卷是否属于平台托管，避免删共享镜像）
	poolPath, _ := h.Virt.GetPoolPath(pool)
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
	cleaned := 0
	tryDeleteVol := func(src string) {
		if src == "" {
			return
		}
		volName, ok := volNameFor(src)
		if !ok || seen[volName] {
			return
		}
		seen[volName] = true
		// libvirt 卷（克隆卷等 root 属主）走 vol-delete；seed 等直接落盘文件 libvirt 不认作卷，os 兜底删文件
		_ = h.Virt.DeleteVolume(pool, volName)
		if poolPath != "" && os.Remove(filepath.Join(poolPath, volName)) == nil {
			cleaned++
		}
	}
	for _, src := range diskSources {
		tryDeleteVol(src)
	}
	tryDeleteVol(filepath.Join(poolPath, vm.Name+".qcow2"))

	// 4. 清理 cloud-init seed 镜像（独立 seed 目录，非池卷）
	if seedDir := config.GlobalConfig.SeedDir; seedDir != "" {
		_ = os.Remove(filepath.Join(seedDir, vm.Name+"-seed.iso"))
	}

	// 5. 软删除数据库记录
	h.DB.Delete(&vm)

	Success(c, gin.H{"message": "虚拟机已删除", "cleaned_vols": cleaned})
}

// GetVMXML 获取虚拟机 XML 定义
func (h *VMHandler) GetVMXML(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	xml, err := h.Virt.GetDomainXML(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": vm.Name, "xml": xml})
}

// UpdateVMXML 更新虚拟机 XML 定义（高级功能）
func (h *VMHandler) UpdateVMXML(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		XML string `json:"xml" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	if err := h.Virt.UpdateDomainXML(vm.Name, req.XML); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": vm.Name, "message": "XML 已更新"})
}

// ListSnapshots 获取虚拟机快照列表（返回 SnapshotInfo 详情数组，含 description/creation_time/state）。
func (h *VMHandler) ListSnapshots(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	snaps, err := h.Virt.ListSnapshots(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, snaps)
}

// CreateSnapshot 创建虚拟机快照（body: {name, description?}）。
func (h *VMHandler) CreateSnapshot(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "快照名称只允许字母、数字、下划线和连字符")
		return
	}

	if err := h.Virt.CreateSnapshot(vm.Name, req.Name, req.Description); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": req.Name})
}

// DeleteSnapshot 删除虚拟机快照
func (h *VMHandler) DeleteSnapshot(c *gin.Context) {
	id := c.Param("id")
	snapName := c.Param("snap")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	if err := h.Virt.DeleteSnapshot(vm.Name, snapName); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": snapName})
}

// RevertSnapshot 回滚虚拟机到指定快照
func (h *VMHandler) RevertSnapshot(c *gin.Context) {
	id := c.Param("id")
	snapName := c.Param("snap")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	if err := h.Virt.RevertSnapshot(vm.Name, snapName); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": snapName})
}

// GetHostInfo 获取宿主机信息
func (h *VMHandler) GetHostInfo(c *gin.Context) {
	hostname, err := exec.Command("hostname").Output()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取主机名失败", err)
		return
	}
	kernel, err := exec.Command("uname", "-r").Output()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取内核版本失败", err)
		return
	}
	cpus, err := exec.Command("nproc").Output()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取 CPU 数量失败", err)
		return
	}
	free, err := exec.Command("free", "-h").Output()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取内存信息失败", err)
		return
	}
	uptime, err := exec.Command("uptime", "-p").Output()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取运行时长失败", err)
		return
	}

	lines := strings.Split(string(free), "\n")
	var memTotal, memUsed string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Mem:") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				memTotal = fields[1]
				memUsed = fields[2]
			}
		}
	}

	c.JSON(200, HostInfo{
		Hostname: strings.TrimSpace(string(hostname)),
		Kernel:   strings.TrimSpace(string(kernel)),
		CPUs:     strings.TrimSpace(string(cpus)),
		MemTotal: memTotal,
		MemUsed:  memUsed,
		Uptime:   strings.TrimSpace(string(uptime)),
	})
}

// ScanImportVMs 扫描宿主机上未被平台纳管的存量域（virsh 已定义、DB 无记录的 VM）。
// 返回全部候选域及其硬件摘要，前端据此勾选导入；libvirt 侧不做任何改动。
func (h *VMHandler) ScanImportVMs(c *gin.Context) {
	host, err := h.firstHost()
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "请先在宿主机管理中登记宿主机", err)
		return
	}

	details, err := h.Virt.ListDomainsWithDetail()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "扫描宿主机虚拟机失败", err)
		return
	}

	uuids, err := h.trackedUUIDs()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询已纳管虚拟机失败", err)
		return
	}

	items := make([]virt.DomainDetail, 0, len(details))
	managed := 0
	for i := range details {
		d := &details[i]
		if uuids[d.UUID] {
			d.Managed = true
			managed++
		}
		d.DiskGB = h.Virt.DiskSizeGB(d.DiskPath)
		items = append(items, *d)
	}

	Success(c, gin.H{
		"host_id":   host.ID,
		"host_name": host.Name,
		"total":     len(items),
		"managed":   managed,
		"unmanaged": len(items) - managed,
		"items":     items,
	})
}

// ImportVMs 将选中的存量域纳入平台纳管：仅在 DB 写入记录，不修改 libvirt 侧定义。
// 已纳管（UUID 已存在）的域自动跳过；导入后状态与 libvirt 实时对齐。
func (h *VMHandler) ImportVMs(c *gin.Context) {
	var req struct {
		HostID uint     `json:"host_id"`
		Names  []string `json:"names"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Names) == 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "请选择要导入的虚拟机", err)
		return
	}

	var host model.Host
	if req.HostID != 0 {
		err := h.DB.First(&host, req.HostID).Error
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "宿主机不存在", err)
			return
		}
	} else {
		hst, err := h.firstHost()
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "请先在宿主机管理中登记宿主机", err)
			return
		}
		host = *hst
	}

	details, err := h.Virt.ListDomainsWithDetail()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "扫描宿主机虚拟机失败", err)
		return
	}
	byName := make(map[string]virt.DomainDetail, len(details))
	for _, d := range details {
		byName[d.Name] = d
	}

	poolPaths, _ := h.poolPathMap()
	inserted, skipped, failed := 0, 0, 0
	var errs []string
	for _, name := range req.Names {
		d, ok := byName[name]
		if !ok {
			failed++
			errs = append(errs, fmt.Sprintf("%s: 域不存在", name))
			continue
		}

		var cnt int64
		h.DB.Unscoped().Model(&model.VM{}).Where("uuid = ?", d.UUID).Count(&cnt)
		if cnt > 0 {
			skipped++
			continue
		}

		pool := ""
		for p, path := range poolPaths {
			if d.DiskPath != "" && strings.HasPrefix(d.DiskPath, path+"/") {
				pool = p
				break
			}
		}

		vm := model.VM{
			UUID:        d.UUID,
			Name:        d.Name,
			HostID:      host.ID,
			Template:    "imported",
			StoragePool: pool,
			VCPU:        d.VCPU,
			MemoryMB:    d.MemoryMB,
			DiskGB:      d.DiskGB,
			MACAddress:  d.MAC,
			OSType:      d.OSType,
			Status:      d.State,
		}
		if err := h.DB.Create(&vm).Error; err != nil {
			failed++
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		inserted++
	}

	Success(c, gin.H{
		"imported": inserted,
		"skipped":  skipped,
		"failed":   failed,
		"errors":   errs,
	})
}

// firstHost 返回平台登记的首台宿主机（默认纳管目标）。
func (h *VMHandler) firstHost() (*model.Host, error) {
	var host model.Host
	if err := h.DB.Order("id ASC").First(&host).Error; err != nil {
		return nil, err
	}
	return &host, nil
}

// trackedUUIDs 返回 DB 中已纳管（含软删除）的全部 VM UUID 集合。
func (h *VMHandler) trackedUUIDs() (map[string]bool, error) {
	var uuids []string
	if err := h.DB.Unscoped().Model(&model.VM{}).Pluck("uuid", &uuids).Error; err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(uuids))
	for _, u := range uuids {
		set[u] = true
	}
	return set, nil
}

// VMCreateOptions 创建虚拟机向导所需的静态选项（对应契约 GET /api/vms/options）。
type VMCreateOptions struct {
	Pools        []string           `json:"pools"`
	Networks     []string           `json:"networks"`
	CloudImages  []model.Image      `json:"cloud_images"`
	OSList       []virt.OSItem      `json:"os_list"`
	StoragePools []virt.PoolInfo    `json:"storage_pools,omitempty"`
	NetInfo      []virt.NetworkInfo `json:"network_info,omitempty"`
}

// GetVMOptions 返回创建虚拟机向导的选项（存储池、网络、云镜像、OS 列表）。
// 供前端创建向导选择使用（对应 virsh 环境的资源枚举）。
func (h *VMHandler) GetVMOptions(c *gin.Context) {
	// 存储池（名称 + 详情）
	pools, err := h.Virt.ListPools()
	if err != nil {
		pools = []string{}
	}
	poolInfos, _ := h.Virt.ListPoolInfos()

	// 网络（名称 + 详情）
	networks := []string{}
	netInfos := []virt.NetworkInfo{}
	if nws, err := h.Virt.ListNetworks(); err == nil {
		netInfos = nws
		for _, n := range nws {
			networks = append(networks, n.Name)
		}
	}

	// 云镜像：镜像管理中标记为模板的（含普通镜像，便于导入安装盘）
	var images []model.Image
	if err := h.DB.Order("created_at desc").Find(&images).Error; err != nil {
		images = []model.Image{}
	}

	Success(c, gin.H{
		"pools":         pools,
		"storage_pools": poolInfos,
		"networks":      networks,
		"network_info":  netInfos,
		"cloud_images":  images,
		"os_list":       virt.OSList,
	})
}

// poolPathMap 返回 存储池名 → 目标路径 的映射（供磁盘归属推断）。
func (h *VMHandler) poolPathMap() (map[string]string, error) {
	names, err := h.Virt.ListPools()
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(names))
	for _, name := range names {
		if path, err := h.Virt.GetPoolPath(name); err == nil {
			m[name] = path
		}
	}
	return m, nil
}
