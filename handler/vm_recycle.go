package handler

// 回收站（批次 N）：VM 删除是软删（model.VM 的 gorm.DeletedAt，GORM 默认查询排除）+
// 域 undefine + 卷按 shouldKeepVol 守卫保留。回收站 = 把软删记录可视化 + 恢复 / 彻底清除。
//
// 语义约定：
//   - 列表/恢复/清除全部仅 admin（每个方法先过 requireAdminRole 二次收口，与 vm_grant.go 同款；
//     路由建议挂 admin 组，本闸作为纵深防御，路由误挂 operator 组时仍拦得住）。
//   - Unscoped() 绕过 GORM 软删过滤：软删行带 deleted_at 非空，不加 Unscoped 的查询/更新
//     会被自动追加的 `deleted_at IS NULL` 条件过滤掉，永远查不到也改不动。
//   - 恢复清空 deleted_at；域仍在则同步实时状态，域已 undefine 且系统盘卷还在时
//     按 DB 记录重建精简定义并 define（B3a 批次，见 redefineRestoredVM），
//     重建失败降级为仅恢复记录，如实告知「定义已不存在」。

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// VMRecycleHandler 回收站处理器。
type VMRecycleHandler struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewVMRecycleHandler 创建回收站处理器。
func NewVMRecycleHandler(db *gorm.DB, v *virt.Virt) *VMRecycleHandler {
	return &VMRecycleHandler{DB: db, Virt: v}
}

// deletedVMItem 回收站列表条目（model.VM 的 DeletedAt 带 json:"-"，需显式结构暴露 deleted_at）。
type deletedVMItem struct {
	ID           uint           `json:"id"`
	Name         string         `json:"name"`
	UUID         string         `json:"uuid"`
	Status       string         `json:"status"`
	StoragePool  string         `json:"storage_pool"`
	DeletedAt    gorm.DeletedAt `json:"deleted_at"`
	DomainExists bool           `json:"domain_exists"`
	// 系统盘是否仍在池中（C 批次）：域不在+盘不在 = 恢复只能捞回一条空记录
	DiskExists bool   `json:"disk_exists"`
	DiskVolume string `json:"disk_volume,omitempty"` // 系统盘卷名（存在时）
	// 是否留有删除时的域定义存档（B 批次）：有存档才能「精确重建」
	// （多盘/固件/光驱/原网络配置全还原），否则只能精简重建
	HasArchive bool `json:"has_archive"`
}

// ListDeleted 回收站列表（admin）。GET /api/vms-recycle?page=&page_size=
// Unscoped 绕过软删过滤取全部记录，再筛 deleted_at 非空（即只在回收站里的）。
// 分页在 DB 层完成：total 为回收站记录总数，items 只含当前页——每行的 libvirt 域状态
// 探测（GetDomainState）只对当前页执行，不再为全量记录逐台查询。
func (h *VMRecycleHandler) ListDeleted(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	page, pageSize := parsePageQuery(c, 20, 100)

	var total int64
	if err := h.DB.Unscoped().Model(&model.VM{}).
		Where("deleted_at IS NOT NULL").Count(&total).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询回收站失败", err)
		return
	}

	var vms []model.VM
	// 按删除时间倒序（最近删除在前），id 兜底——分页必须有确定序，否则同一行可能跨页重复或漏出
	if err := h.DB.Unscoped().Model(&model.VM{}).
		Where("deleted_at IS NOT NULL").
		Order("deleted_at DESC, id DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&vms).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询回收站失败", err)
		return
	}
	items := make([]deletedVMItem, 0, len(vms))
	for _, vm := range vms {
		// 该 VM 的域在 libvirt 是否仍存在：能查到状态即存在（对应 virsh domstate）。
		// 查询失败（含 libvirt 不可达）一律按不存在呈现，不阻断整个列表。
		_, err := h.Virt.GetDomainState(vm.Name)
		diskPath, pool := h.systemDiskPath(vm)
		item := deletedVMItem{
			ID:           vm.ID,
			Name:         vm.Name,
			UUID:         vm.UUID,
			Status:       vm.Status,
			StoragePool:  vm.StoragePool,
			DeletedAt:    vm.DeletedAt,
			DomainExists: err == nil,
			DiskExists:   diskPath != "",
			HasArchive:   strings.TrimSpace(vm.DomainXML) != "",
		}
		if diskPath != "" {
			item.DiskVolume = filepath.Base(diskPath)
			if item.StoragePool == "" {
				item.StoragePool = pool
			}
		}
		items = append(items, item)
	}
	Success(c, gin.H{"total": total, "page": page, "page_size": pageSize, "items": items})
}

// findDeletedVM 按 path 主键取回收站记录：paramID 解析 + Unscoped 查询 + 状态校验一体。
// 只接受 deleted_at 非空的记录——恢复/清除都对活着的 VM 无意义，硬删更是灾难。
func (h *VMRecycleHandler) findDeletedVM(c *gin.Context) (model.VM, bool) {
	id, ok := paramID(c, "id")
	if !ok {
		return model.VM{}, false
	}
	var vm model.VM
	if err := h.DB.Unscoped().First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "回收站中不存在该虚拟机", err)
		return model.VM{}, false
	}
	if !vm.DeletedAt.Valid {
		Fail(c, http.StatusBadRequest, "该虚拟机不在回收站中")
		return model.VM{}, false
	}
	return vm, true
}

// Restore 恢复回收站虚拟机（admin）。POST /api/vms-recycle/:id/restore
// 清空 deleted_at 使记录回到正常列表；域仍在则同步实时状态；域已 undefine 且系统盘卷还在
// 则按 DB 记录重建精简定义（恢复过的 VM 可直接开机），重建失败降级为仅恢复记录。
func (h *VMRecycleHandler) Restore(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	vm, ok := h.findDeletedVM(c)
	if !ok {
		return
	}
	// 恢复含重 define 域（域已 undefine 时），与生命周期写操作互斥（AGENTS 条款 16）
	if !guardVMIdle(c, h.DB, vm.ID) {
		return
	}
	release, ok := lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()
	// 同名活记录防撞：软删期间可能用同名重建过机器，恢复会让列表出现两台同名 VM，
	// libvirt 域操作按名字寻址会打到别人头上。
	var cnt int64
	if err := h.DB.Model(&model.VM{}).Where("name = ? AND id <> ?", vm.Name, vm.ID).Count(&cnt).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "恢复前检查失败", err)
		return
	}
	if cnt > 0 {
		Fail(c, http.StatusConflict, "已存在同名虚拟机，无法恢复，请先处理同名记录")
		return
	}

	if err := h.DB.Unscoped().Model(&vm).Update("deleted_at", nil).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "恢复虚拟机记录失败", err)
		return
	}

	// 域仍在：同步一次实时状态（删除流程 undefine 成功才会软删，域存在多为异常残留，
	// 比如任务中途失败；如实呈现比假装「干净恢复」更有用）。
	// 状态回写走 dbx：写失败会让回收站记录的状态与 libvirt 实际状态分叉，
	// 且没有第二次同步机会（列表会一直显示旧状态），必须重试 + 醒目留痕。
	// 不回滚恢复本身（记录已恢复成功），但如实告知前端「状态未落库」，别让前端拿它当已同步。
	state, err := h.Virt.GetDomainState(vm.Name)
	if err == nil {
		msg := "已恢复（libvirt 中仍存在同名域，状态已同步）"
		if err := dbx.Persist(h.DB, fmt.Sprintf("回收站恢复状态同步 vm=%s", vm.Name), func() error {
			return h.DB.Model(&vm).Update("status", state).Error
		}); err != nil {
			msg = fmt.Sprintf("已恢复，但状态回写失败（%s），列表状态可能与实际不一致，请刷新重试", state)
		}
		Success(c, gin.H{
			"restored":       true,
			"domain_defined": true,
			"status":         state,
			"message":        msg,
		})
		return
	}

	// 域已不存在（删除时已 undefine）：优先按删除时留存的域定义存档精确重建（B），
	// 无存档或存档重建失败时退回按 DB 记录精简重建；任何失败降级为仅恢复记录，
	// 不阻断恢复本身。
	outcome, rerr := h.redefineRestoredVM(vm)
	if rerr != nil {
		LogError(c, rerr)
		Success(c, gin.H{
			"restored":       true,
			"domain_defined": false,
			"message":        "记录已恢复，但虚拟机定义无法重建（系统盘卷可能已删除），可在创建向导用同名卷重新定义",
		})
		return
	}
	if err := h.DB.Model(&vm).Update("status", model.VMStatusShutOff).Error; err != nil {
		LogError(c, err)
	}
	msg := "已恢复并重新定义虚拟机（按删除时的域定义存档精确重建，配置与删除前一致）"
	switch outcome.Mode {
	case "exact_missing":
		msg = fmt.Sprintf("已恢复并重新定义虚拟机（按存档重建），但以下磁盘文件已不存在：%s —— 开机前请补齐或移除该磁盘",
			strings.Join(outcome.Missing, "、"))
	case "minimal":
		msg = "已恢复并重新定义虚拟机（无删除时存档，按数据库记录重建精简定义：单系统盘 + 默认 NAT 网卡，多盘需在磁盘管理手动挂回）"
	}
	Success(c, gin.H{
		"restored":       true,
		"domain_defined": true,
		"status":         model.VMStatusShutOff,
		"restore_mode":   outcome.Mode,
		"message":        msg,
	})
}

// systemDiskPath 探测 VM 的系统盘卷路径（返回 路径, 所在池名）。
// 先按记录池位找，未命中再跨池兜底——VM.StoragePool 字段可能为空或与磁盘实际所在池
// 不符（删卷守卫、手工建机、迁移后残留等），只认单池会让「盘明明还在却恢复不了」。
func (h *VMRecycleHandler) systemDiskPath(vm model.VM) (string, string) {
	pool := vm.StoragePool
	if pool != "" {
		if poolPath, err := h.Virt.GetPoolPath(pool); err == nil {
			if disk, ok := probeSystemDisk(poolPath, vm.Name); ok {
				return disk, pool
			}
		}
	}
	pools, err := h.Virt.ListPools()
	if err != nil {
		if pool == "" {
			pool = tasks.DefaultStoragePoolResolver()
		}
		return "", pool
	}
	for _, pn := range pools {
		if pn == pool {
			continue
		}
		pp, err := h.Virt.GetPoolPath(pn)
		if err != nil {
			continue
		}
		if disk, ok := probeSystemDisk(pp, vm.Name); ok {
			return disk, pn
		}
	}
	if pool == "" {
		pool = tasks.DefaultStoragePoolResolver()
	}
	return "", pool
}

// probeSystemDisk 按建卷命名约定探测系统盘：新建机 <名>.qcow2 / 克隆机 <名>-diska.qcow2
// / 镜像建机 <名>-sys.qcow2（见 execDeleteVM 的兜底与 execCloneImageVM）。
func probeSystemDisk(poolPath, vmName string) (string, bool) {
	for _, cand := range []string{
		vmName + ".qcow2",
		vmName + "-diska.qcow2",
		vmName + "-sys.qcow2",
	} {
		p := filepath.Join(poolPath, cand)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

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

// Purge 彻底清除回收站虚拟机（admin）。DELETE /api/vms-recycle/:id/purge?purge_volumes=true
// 默认只物理删除数据库行；?purge_volumes=true 额外尝试删除其存储池里以 VM 名命名的
// 系统盘文件（<vm名>.qcow2）——删 VM 时的 shouldKeepVol 守卫刻意保留的共享卷，
// 这里必须重过守卫，命中引用照样保留（宁可留垃圾文件，不可损坏在用磁盘）。
func (h *VMRecycleHandler) Purge(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	vm, ok := h.findDeletedVM(c)
	if !ok {
		return
	}
	// purge_volumes 删盘文件，守卫数据基于当下快照，须与克隆/删除/热插拔互斥
	if !guardVMIdle(c, h.DB, vm.ID) {
		return
	}
	release, ok := lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()
	purgeVolumes := c.Query("purge_volumes") == "true"

	removed := 0
	var kept []string
	if purgeVolumes {
		removed, kept = h.purgeVolFile(c, vm)
	}

	if err := h.DB.Unscoped().Delete(&vm).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "彻底清除虚拟机记录失败", err)
		return
	}
	Success(c, gin.H{
		"purged":          true,
		"volumes_removed": removed,
		"volumes_kept":    kept,
		"message":         "已彻底清除虚拟机记录",
	})
}

// purgeVolFile 清理回收站 VM 在存储池里的系统盘文件（<vm名>.qcow2）。
// 返回删除数与保留原因列表。守卫与 execDeleteVM 的 shouldKeepVol 同一立场：
// 池路径拿不到 / 文件被镜像库登记 / 仍被子卷当 backing 父盘，一律不删。
func (h *VMRecycleHandler) purgeVolFile(c *gin.Context, vm model.VM) (removed int, kept []string) {
	pool := vm.StoragePool
	if pool == "" {
		pool = tasks.DefaultStoragePoolResolver()
	}
	poolPath, err := h.Virt.GetPoolPath(pool)
	if err != nil {
		// 池路径拿不到就无法判定「文件确属该池」，宁可不删
		LogError(c, err)
		kept = append(kept, vm.Name+".qcow2（存储池 "+pool+" 路径获取失败，保守跳过）")
		return 0, kept
	}

	volPath := filepath.Join(poolPath, vm.Name+".qcow2")
	if _, err := os.Stat(volPath); err != nil {
		return 0, nil // 文件不存在（删除时已清掉），无事可做
	}

	// 守卫：镜像库登记的共享基镜像（POST /api/images/register 可把任意池卷登记为云镜像，
	// 恰好叫 <vm名>.qcow2 并非不可能）
	var imgCnt int64
	if err := h.DB.Model(&model.Image{}).Where("path = ?", volPath).Count(&imgCnt).Error; err != nil {
		LogError(c, err)
		kept = append(kept, vm.Name+".qcow2（镜像库查询失败，保守跳过）")
		return 0, kept
	}
	if imgCnt > 0 {
		kept = append(kept, vm.Name+".qcow2（已登记为镜像库镜像）")
		return 0, kept
	}

	// 守卫：仍是其他卷的 qcow2 backing 父盘（增量克隆链）
	refs, err := h.Virt.ListBackingRefs(pool)
	if err != nil {
		// 枚举失败按空守卫处理会漏判，故整体保守跳过（与删卷守卫同一立场）
		LogError(c, err)
		kept = append(kept, vm.Name+".qcow2（backing 引用枚举失败，保守跳过）")
		return 0, kept
	}
	if children := refs[volPath]; len(children) > 0 {
		kept = append(kept, vm.Name+".qcow2（是增量克隆父盘，仍被 "+
			strings.Join(children, "、")+" 依赖）")
		return 0, kept
	}

	if err := os.Remove(volPath); err != nil {
		LogError(c, err)
		kept = append(kept, vm.Name+".qcow2（删除失败）")
		return 0, kept
	}
	return 1, nil
}
