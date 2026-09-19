package handler

// 回收站（批次 N）：VM 删除是软删（model.VM 的 gorm.DeletedAt，GORM 默认查询排除）+
// 域 undefine + 卷按 shouldKeepVol 守卫保留。回收站 = 把软删记录可视化 + 恢复 / 彻底清除。
//
// 语义约定：
//   - 列表/恢复/清除全部仅 admin（每个方法先过 requireAdminRole 二次收口，与 vm_grant.go 同款；
//     路由建议挂 admin 组，本闸作为纵深防御，路由误挂 operator 组时仍拦得住）。
//   - Unscoped() 绕过 GORM 软删过滤：软删行带 deleted_at 非空，不加 Unscoped 的查询/更新
//     会被自动追加的 `deleted_at IS NULL` 条件过滤掉，永远查不到也改不动。
//   - 恢复只还原数据库记录，不重建 libvirt 域——删除时已 undefine，域若还在则顺带同步状态，
//     不在则诚实告知「定义已不存在」，前端引导走创建向导用同名卷重新定义。

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
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
}

// ListDeleted 回收站列表（admin）。GET /api/vms-recycle
// Unscoped 绕过软删过滤取全部记录，再筛 deleted_at 非空（即只在回收站里的）。
func (h *VMRecycleHandler) ListDeleted(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	var vms []model.VM
	if err := h.DB.Unscoped().Model(&model.VM{}).
		Where("deleted_at IS NOT NULL").Find(&vms).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询回收站失败", err)
		return
	}
	items := make([]deletedVMItem, 0, len(vms))
	for _, vm := range vms {
		// 该 VM 的域在 libvirt 是否仍存在：能查到状态即存在（对应 virsh domstate）。
		// 查询失败（含 libvirt 不可达）一律按不存在呈现，不阻断整个列表。
		_, err := h.Virt.GetDomainState(vm.Name)
		items = append(items, deletedVMItem{
			ID:           vm.ID,
			Name:         vm.Name,
			UUID:         vm.UUID,
			Status:       vm.Status,
			StoragePool:  vm.StoragePool,
			DeletedAt:    vm.DeletedAt,
			DomainExists: err == nil,
		})
	}
	Success(c, gin.H{"total": len(items), "items": items})
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
// 清空 deleted_at 使记录回到正常列表；域仍在则同步实时状态，域已 undefine 则如实告知。
func (h *VMRecycleHandler) Restore(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	vm, ok := h.findDeletedVM(c)
	if !ok {
		return
	}
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
	state, err := h.Virt.GetDomainState(vm.Name)
	if err != nil {
		Success(c, gin.H{
			"restored":       true,
			"domain_defined": false,
			"message":        "记录已恢复，但虚拟机定义已不存在，可在创建向导用同名卷重新定义",
		})
		return
	}
	if err := h.DB.Model(&vm).Update("status", state).Error; err != nil {
		// 状态同步失败不回滚恢复本身，完整错误进日志即可
		LogError(c, err)
	}
	Success(c, gin.H{
		"restored":       true,
		"domain_defined": true,
		"status":         state,
		"message":        "已恢复（libvirt 中仍存在同名域，状态已同步）",
	})
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
