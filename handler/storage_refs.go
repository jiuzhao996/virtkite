package handler

// storage_refs.go 存储卷的在用引用与删除守卫：卷级引用统计（挂载 VM / 镜像库登记 / 增量克隆
// 子卷）、引用查询端点、删卷守卫、孤儿卷清理任务提交与拒绝原因文案。
// 与 storage.go（存储池与卷的 CRUD）分工：本文件只管「这个卷能不能删」。

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
)

// VolumeRefs 单个存储卷的在用引用信息（删卷守卫与"在用"徽标共用一份数据）。
type VolumeRefs struct {
	VMs      []string `json:"vms"`      // 直接挂载该卷的虚拟机名
	Images   []string `json:"images"`   // 镜像库中登记该路径的镜像名
	Children []string `json:"children"` // 以该卷为 backing 父盘的子卷名（增量克隆）
}

// inUse 该卷是否仍被任何资源引用。
func (r *VolumeRefs) inUse() bool {
	return len(r.VMs) > 0 || len(r.Images) > 0 || len(r.Children) > 0
}

// poolVolumeRefs 计算存储池内每个卷的在用引用：虚拟机挂载 / 镜像库登记 / 增量克隆子卷。
// 池级一次算全，供引用查询端点与删卷守卫共用（对应 virsh vol-dumpxml + domblklist + vol-list）。
func (h *StorageHandler) poolVolumeRefs(poolName string) (map[string]*VolumeRefs, error) {
	pool, err := h.Virt.GetPoolInfo(poolName)
	if err != nil {
		return nil, err
	}
	// 父盘路径 → 子卷名列表（增量克隆 backing 链）；失败按空处理，不让守卫因枚举失败而失效
	backing, _ := h.Virt.ListBackingRefs(poolName)
	disks, _ := h.Virt.ListAllDomainDiskSources()

	// 镜像库登记一次查回（原循环内逐卷查是 N+1，卷多时 volume-refs 明显变慢）
	imgByPath := map[string][]string{}
	if h.DB != nil && len(pool.Volumes) > 0 {
		paths := make([]string, 0, len(pool.Volumes))
		for _, vol := range pool.Volumes {
			paths = append(paths, vol.Path)
		}
		var images []model.Image
		if err := h.DB.Where("path IN ?", paths).Find(&images).Error; err == nil {
			for _, im := range images {
				imgByPath[im.Path] = append(imgByPath[im.Path], im.Name)
			}
		}
	}

	refs := make(map[string]*VolumeRefs, len(pool.Volumes))
	for _, vol := range pool.Volumes {
		r := &VolumeRefs{}
		// 虚拟机挂载：域磁盘 source 精确命中卷路径
		for domName, paths := range disks {
			for _, p := range paths {
				if p == vol.Path {
					r.VMs = append(r.VMs, domName)
					break
				}
			}
		}
		// 镜像库登记：images.path 保存的是落盘绝对路径，精确匹配
		r.Images = imgByPath[vol.Path]
		// 增量克隆子卷
		r.Children = backing[vol.Path]
		refs[vol.Name] = r
	}
	return refs, nil
}

// GetVolumeRefs 查询存储池内所有卷的在用引用（GET /api/storage/pools/:name/volume-refs）。
// 前端卷管理弹窗据此显示"在用"徽标，删卷确认文案带上引用详情。
func (h *StorageHandler) GetVolumeRefs(c *gin.Context) {
	poolName := c.Param("name")
	refs, err := h.poolVolumeRefs(poolName)
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, err)
		return
	}
	Success(c, gin.H{"pool": poolName, "refs": refs})
}

// DeleteVolume 删除存储池中的卷。
// 删除前过在用守卫：仍被虚拟机挂载 / 已登记镜像库 / 是增量克隆父盘的卷一律拒绝删除，
// 与 tasks.execDeleteVM 的 shouldKeepVol 三重守卫同一立场——宁可删不掉，不可损坏在用磁盘。
func (h *StorageHandler) DeleteVolume(c *gin.Context) {
	poolName := c.Param("name")
	volName := c.Param("vol")
	// 与 CleanupOrphans/CreateVolume 一致走白名单：两个名字都会进 libvirt 的 XML，
	// 缺校验时 <?>、引号等元字符可被用来构造非法的池/卷名查询。
	if !validVolName(poolName) {
		Fail(c, http.StatusBadRequest, "存储池名称只允许字母、数字、下划线、连字符和点")
		return
	}
	if !validVolName(volName) {
		Fail(c, http.StatusBadRequest, "卷名称只允许字母、数字、下划线、连字符和点")
		return
	}

	refs, err := h.poolVolumeRefs(poolName)
	if err != nil {
		// 引用查询失败必须拒绝删除：查不到引用≠没有引用（libvirt 或镜像库查询超时/中断时
		// 会漏判在用卷），带着这个不确定性删除等于放行误删，比删不掉的风险大得多。
		ErrorWithMessage(c, http.StatusInternalServerError, "无法确认卷的引用情况，为避免误删已拒绝本次删除，请稍后重试", err)
		return
	}
	if r, ok := refs[volName]; ok && r.inUse() {
		Fail(c, http.StatusConflict, volumeInUseReason(volName, r))
		return
	}

	if err := h.Virt.DeleteVolume(poolName, volName); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"pool": poolName, "vol": volName})
}

// CleanupOrphans 清理存储池孤儿卷（POST /api/storage/pools/:name/orphan-cleanup，转后台任务）。
// 孤儿 = 不被虚拟机挂载、未登记镜像库、也非任何子卷 backing 父盘的卷；判定由任务执行器完成，
// 前端确认弹窗先经 volume-refs 端点列出候选。任务结果含 deleted/kept 明细。
func (h *StorageHandler) CleanupOrphans(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	poolName := c.Param("name")
	if !validVolName(poolName) {
		Fail(c, http.StatusBadRequest, "存储池名称只允许字母、数字、下划线、连字符和点")
		return
	}
	userID, _ := c.Get("user_id")
	username, _ := c.Get("username")
	var uid *uint
	if v, ok := userID.(uint); ok {
		uid = &v
	}
	uname, _ := username.(string)

	task, err := h.Tasks.Submit("cleanup_volumes", "清理孤儿卷 "+poolName,
		gin.H{"pool": poolName}, uid, uname, "", nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	Accepted(c, "清理任务已提交", gin.H{"task_id": task.ID})
}

// volumeInUseReason 生成删卷守卫的中文拒绝原因（逐类列出引用方）。
func volumeInUseReason(volName string, r *VolumeRefs) string {
	var reasons []string
	if len(r.VMs) > 0 {
		reasons = append(reasons, fmt.Sprintf("仍被虚拟机挂载（%s），请先卸载磁盘或删除对应虚拟机", strings.Join(r.VMs, "、")))
	}
	if len(r.Images) > 0 {
		reasons = append(reasons, fmt.Sprintf("已登记为镜像库镜像（%s），请先在镜像管理中删除该镜像", strings.Join(r.Images, "、")))
	}
	if len(r.Children) > 0 {
		reasons = append(reasons, fmt.Sprintf("是增量克隆父盘，仍被 %d 个子卷依赖（%s）", len(r.Children), strings.Join(r.Children, "、")))
	}
	return fmt.Sprintf("卷 %s 正在使用中，已阻止删除: %s", volName, strings.Join(reasons, "；"))
}
