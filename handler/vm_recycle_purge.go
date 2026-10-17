// 本文件负责回收站「彻底清除」（Purge）：物理删除数据库行，可选（?purge_volumes=true）
// 额外删除存储池里以 VM 名命名的系统盘文件。删卷前重过与 execDeleteVM 的 shouldKeepVol
// 同源的四重守卫（池路径、镜像库共享基镜像、增量克隆父盘、仍被某域挂载），命中即保留。
// 回收站列表/恢复入口见 vm_recycle.go，域定义重建见 vm_recycle_restore.go。
package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
)

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

	// 彻底清除必须连 libvirt 域一起处理：删除任务可能因异常停在 undefine 之后、
	// 软删之前（或域是手工 define 的同名残留），只删 DB 行会留下平台再也管不到的域。
	// 域已不存在时容错（与 execDeleteVM 同口径）。
	if err := h.Virt.UndefineDomain(vm.Name); err != nil && !virt.IsDomainNotFound(err) {
		ErrorWithMessage(c, http.StatusInternalServerError, "删除虚拟机定义失败", err)
		return
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

// domainHoldingVol 返回仍把 volPath 当磁盘源挂着的域名（空串=无人挂载，可删）。
// 抽成纯函数是为了能单测：这是「回收站连盘彻底清除」路径上唯一能挡住误删在用磁盘的判定。
func domainHoldingVol(disks map[string][]string, volPath string) string {
	for domName, paths := range disks {
		for _, p := range paths {
			if p == volPath {
				return domName
			}
		}
	}
	return ""
}

// purgeVolFile 清理回收站 VM 在存储池里的系统盘文件。
// 卷名按建机/克隆的三套约定逐个尝试：<vm>.qcow2、<vm>-diska.qcow2、<vm>-sys.qcow2
// （create_vm 新建盘、clone_vm、clone_image_vm 分别用这三种命名；旧实现只认第一种，
// 导致克隆机彻底清除后磁盘文件残留）。返回删除数与保留原因列表。
func (h *VMRecycleHandler) purgeVolFile(c *gin.Context, vm model.VM) (removed int, kept []string) {
	pool := vm.StoragePool
	if pool == "" {
		pool = tasks.DefaultStoragePoolResolver()
	}
	poolPath, err := h.Virt.GetPoolPath(pool)
	if err != nil {
		// 池路径拿不到就无法判定「文件确属该池」，宁可不删
		LogError(c, err)
		return 0, []string{vm.Name + ".qcow2（存储池 " + pool + " 路径获取失败，保守跳过）"}
	}
	for _, fileName := range []string{vm.Name + ".qcow2", vm.Name + "-diska.qcow2", vm.Name + "-sys.qcow2"} {
		n, k := h.purgeOneVolFile(c, pool, poolPath, fileName)
		removed += n
		kept = append(kept, k...)
	}
	return removed, kept
}

// purgeOneVolFile 删除单个卷文件。四重守卫，任一命中即保留并给出中文原因：
//  1. 池路径拿不到（无法判定文件确属该池）
//  2. 文件被镜像库登记为共享基镜像
//  3. 文件仍是其他卷的 backing 父盘（增量克隆链）
//  4. 文件仍被某个域当磁盘源挂着——回收站场景特有的那条，见下方注释
//
// 立场与 execDeleteVM 的 shouldKeepVol 一致：宁可留一个垃圾文件，不可损坏在用磁盘。
func (h *VMRecycleHandler) purgeOneVolFile(c *gin.Context, pool, poolPath, fileName string) (removed int, kept []string) {
	volPath := filepath.Join(poolPath, fileName)
	if _, err := os.Stat(volPath); err != nil {
		return 0, nil // 文件不存在（删除时已清掉），无事可做
	}

	// 守卫：镜像库登记的共享基镜像（POST /api/images/register 可把任意池卷登记为云镜像，
	// 恰好叫 <vm名>.qcow2 并非不可能）
	var imgCnt int64
	if err := h.DB.Model(&model.Image{}).Where("path = ?", volPath).Count(&imgCnt).Error; err != nil {
		LogError(c, err)
		kept = append(kept, fileName+"（镜像库查询失败，保守跳过）")
		return 0, kept
	}
	if imgCnt > 0 {
		kept = append(kept, fileName+"（已登记为镜像库镜像）")
		return 0, kept
	}

	// 守卫：仍是其他卷的 qcow2 backing 父盘（增量克隆链）
	refs, err := h.Virt.ListBackingRefs(pool)
	if err != nil {
		// 枚举失败按空守卫处理会漏判，故整体保守跳过（与删卷守卫同一立场）
		LogError(c, err)
		kept = append(kept, fileName+"（backing 引用枚举失败，保守跳过）")
		return 0, kept
	}
	if children := refs[volPath]; len(children) > 0 {
		kept = append(kept, fileName+"（是增量克隆父盘，仍被 "+
			strings.Join(children, "、")+" 依赖）")
		return 0, kept
	}

	// 守卫：该路径仍被**某个域**当磁盘源挂着。
	// VolumeGuard 刻意没有这一条——删 VM 时它自己的域磁盘当然不该算「在用」；但回收站
	// 场景不同：记录是软删的、域多半已经没了，而「删掉同名 VM 后又建了一台同名 VM」
	// 是正常操作，后者的磁盘路径与前者完全相同（<池>/<名>.qcow2）。少了这条守卫，
	// 彻底清除旧记录会把正在运行的新 VM 的磁盘 unlink 掉——QEMU 还握着已删除的 inode
	// 继续写，重启即数据全失。宁可留一个垃圾文件。
	disks, derr := h.Virt.ListAllDomainDiskSources()
	if derr != nil {
		LogError(c, derr)
		kept = append(kept, fileName+"（域磁盘清单枚举失败，保守跳过）")
		return 0, kept
	}
	if domName := domainHoldingVol(disks, volPath); domName != "" {
		kept = append(kept, fileName+"（仍被虚拟机 "+domName+" 挂载，不可删除）")
		return 0, kept
	}

	if err := os.Remove(volPath); err != nil {
		LogError(c, err)
		kept = append(kept, fileName+"（删除失败）")
		return 0, kept
	}
	return 1, nil
}
