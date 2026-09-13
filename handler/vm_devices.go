package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// AttachDisk 热插拔磁盘（对应 virsh attach-device，运行中生效并落配置）。
// body: {disk: DiskSpec}；disk.Target 为空时按 bus 自动分配（NextDiskTarget）。
func (h *VMHandler) AttachDisk(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
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

// QuickAttachDisk 一键添加数据盘：在存储池创建 qcow2 卷并挂载到虚拟机（对应
// virsh vol-create-as + attach-device 两步合一），免手工建卷再填路径。
// body 可空：{size_gb（缺省 20）, pool（缺省用虚拟机记录的 storage_pool）}。
// 卷名 <vm名>-dN.qcow2，N 从现有数据盘数顺延并跳过池内已占用的名字，避免重名冲突。
func (h *VMHandler) QuickAttachDisk(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	var req struct {
		SizeGB int    `json:"size_gb"`
		Pool   string `json:"pool"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if req.SizeGB == 0 {
		req.SizeGB = 20
	}
	if req.SizeGB < 1 || req.SizeGB > 4096 {
		Fail(c, http.StatusBadRequest, "磁盘容量需在 1-4096 GB 之间")
		return
	}
	if req.Pool == "" {
		req.Pool = vm.StoragePool
	}
	if req.Pool == "" {
		Fail(c, http.StatusBadRequest, "虚拟机未记录存储池，请在请求中指定 pool")
		return
	}

	spec, err := h.Virt.GetDomainSpec(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	poolPath, err := h.Virt.GetPoolPath(req.Pool)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "存储池不存在或不可用", err)
		return
	}
	// 已占用卷名集合（同池重名会建卷失败，先查后建）
	existing := map[string]bool{}
	if info, err := h.Virt.GetPoolInfo(req.Pool); err == nil {
		for _, vol := range info.Volumes {
			existing[vol.Name] = true
		}
	}

	// 命名：<vm名>-dN，N 从现有数据盘数 +1 起顺延，撞名继续 +1
	idx := 1
	for _, d := range spec.Disks {
		if d.Device == "disk" {
			idx++
		}
	}
	volName := fmt.Sprintf("%s-d%d", vm.Name, idx)
	for existing[volName+".qcow2"] {
		idx++
		volName = fmt.Sprintf("%s-d%d", vm.Name, idx)
	}

	if _, err := h.Virt.CreateVolume(req.Pool, volName, req.SizeGB); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	disk := virt.DiskSpec{
		Type:   "file",
		Device: "disk",
		Driver: "qcow2",
		Bus:    "virtio",
		Source: fmt.Sprintf("%s/%s.qcow2", strings.TrimRight(poolPath, "/"), volName),
		Target: virt.NextDiskTarget(spec, "virtio"),
	}
	if err := h.Virt.AttachDisk(vm.Name, disk); err != nil {
		// 挂载失败回滚刚建的卷，避免留孤儿卷
		_ = h.Virt.DeleteVolume(req.Pool, volName+".qcow2")
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "disk": disk, "volume": volName + ".qcow2", "pool": req.Pool, "size_gb": req.SizeGB})
}

// EnsureStandardDevices 补齐标准设备（guest-agent 通道 + virtio-rng），幂等。
// 用于把存量虚拟机配置对齐到新装机的标准设备集；响应返回本次实际添加的设备列表。
func (h *VMHandler) EnsureStandardDevices(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	attached, err := h.Virt.EnsureStandardDevices(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if len(attached) == 0 {
		Success(c, gin.H{"vm": vm.Name, "attached": attached, "message": "已是标准配置，无需补齐"})
		return
	}
	Success(c, gin.H{"vm": vm.Name, "attached": attached, "message": "已补齐：" + strings.Join(attached, "、")})
}

// detachVolKeepReason 分离磁盘后删除单卷的安全守卫判定（纯函数，供单测）。
// src 为磁盘源文件绝对路径；managedImages 是镜像库登记的路径集合；
// backingChildren 是以 src 为 backing 父盘的子卷名列表；mountedBy 是仍挂载该路径的其他虚拟机名。
// 返回保留原因（空串 = 可以删）。与 tasks.execDeleteVM 的 shouldKeepVol 三重守卫、
// StorageHandler.DeleteVolume 的在用守卫同一立场：宁可留一个文件，不可损坏共享数据。
func detachVolKeepReason(src string, managedImages map[string]bool, backingChildren, mountedBy []string) string {
	// 守卫 a：镜像库登记的共享基镜像（「基于云镜像创建」对 source path 是直接引用不拷贝，
	// 删掉会让其他引用它的 VM 磁盘损坏，且 images 表留下悬挂记录）
	if managedImages[src] {
		return "是镜像库登记的共享镜像，请先在镜像管理中删除该镜像"
	}
	// 守卫 b：增量克隆父盘（子卷只存增量，父盘一删 backing chain 断裂，所有子机磁盘不可读）
	if len(backingChildren) > 0 {
		return fmt.Sprintf("是增量克隆父盘，仍被 %d 个子卷依赖（%s）",
			len(backingChildren), strings.Join(backingChildren, "、"))
	}
	// 守卫 c：仍被其他虚拟机挂载（同一文件挂多台机是合法操作，删文件会损坏对方磁盘）
	if len(mountedBy) > 0 {
		return fmt.Sprintf("仍被其他虚拟机挂载（%s），请先分离对应虚拟机的磁盘", strings.Join(mountedBy, "、"))
	}
	return ""
}

// DetachDisk 移除磁盘（对应 virsh detach-device，按 target dev 匹配）。
// 可选 query 参数 delete_volume=true：分离成功后同时删除对应存储卷（默认 false = 仅分离，行为不变）。
// 删卷守卫（命中即保留并在 keep_reason 说明）：cdrom 安装介质为共享资源只分离不删；
// 镜像库登记的共享镜像、增量克隆父盘、仍被其他虚拟机挂载的卷一律保留。
// 池外文件不属于平台托管，但既然是显式删卷指令仍然删除（守卫 a/b 优先于该规则）。
// 响应：{vm, target, volume_deleted, volume, keep_reason（未删时）}。
func (h *VMHandler) DetachDisk(c *gin.Context) {
	target := c.Param("target")
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	deleteVolume := c.Query("delete_volume") == "true"

	// 需要删卷时先取分离前的 spec，确定该 target 的磁盘源路径与设备类型；
	// 拿不到磁盘信息就无法安全删卷，此时直接失败而非「只分离不删」让用户误以为卷已删。
	var disk *virt.DiskSpec
	if deleteVolume {
		spec, err := h.Virt.GetDomainSpec(vm.Name)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		for i := range spec.Disks {
			if spec.Disks[i].Target == target {
				disk = &spec.Disks[i]
				break
			}
		}
		if disk == nil {
			Fail(c, http.StatusNotFound, "虚拟机 "+vm.Name+" 上未找到磁盘设备 "+target)
			return
		}
	}

	if err := h.Virt.DetachDisk(vm.Name, target); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 默认行为不变：仅分离
	if !deleteVolume {
		Success(c, gin.H{"vm": vm.Name, "target": target})
		return
	}

	resp := gin.H{"vm": vm.Name, "target": target, "volume_deleted": false, "volume": ""}

	// cdrom（ISO 安装介质）是共享资源，只分离不删卷
	if disk.Device == "cdrom" {
		resp["volume"] = disk.Source
		resp["keep_reason"] = "cdrom 为共享安装介质（ISO），仅分离不删除卷"
		Success(c, resp)
		return
	}
	// 磁盘没有源文件路径（不应出现，防御性兜底）
	if disk.Source == "" {
		resp["keep_reason"] = "磁盘无源文件路径，没有可删除的卷"
		Success(c, resp)
		return
	}
	src := disk.Source
	resp["volume"] = src

	// 守卫 c 前置：文件已不存在视为已删，正常返回
	if _, err := os.Stat(src); os.IsNotExist(err) {
		resp["volume_deleted"] = true
		Success(c, resp)
		return
	}

	// 守卫 a 的数据：镜像库登记的路径集合（软删记录不算，GORM 默认排除 DeletedAt）
	managedImages := map[string]bool{}
	var imgs []model.Image
	if err := h.DB.Select("path").Find(&imgs).Error; err != nil {
		// 读不到镜像库时宁可不删：跳过删卷并说明原因，避免误删登记中的共享镜像
		LogError(c, fmt.Errorf("分离磁盘后读取镜像库失败，跳过删卷 src=%s: %w", src, err))
		resp["keep_reason"] = "读取镜像库失败，为安全起见未删除卷"
		Success(c, resp)
		return
	}
	for _, img := range imgs {
		if img.Path != "" {
			managedImages[img.Path] = true
		}
	}

	// 守卫 b 的数据 + 定位卷所属池：遍历所有池，枚举各池卷的 backing 引用；
	// 未激活的池枚举卷会报错，按无引用跳过（与 tasks 包用法一致）。
	var backingChildren []string
	ownerPool := ""
	pools, _ := h.Virt.ListPools()
	for _, pool := range pools {
		if refs, err := h.Virt.ListBackingRefs(pool); err == nil {
			backingChildren = append(backingChildren, refs[src]...)
		}
		if ownerPool == "" {
			if poolPath, err := h.Virt.GetPoolPath(pool); err == nil && poolPath != "" &&
				strings.HasPrefix(src, strings.TrimRight(poolPath, "/")+"/") {
				ownerPool = pool
			}
		}
	}

	// 守卫 c 的数据：分离后其他虚拟机是否仍挂载该路径（ListAllDomainDiskSources 只统计 device='disk'）
	var mountedBy []string
	if disksByVM, err := h.Virt.ListAllDomainDiskSources(); err == nil {
		for vmName, paths := range disksByVM {
			if vmName == vm.Name {
				continue // 本机磁盘刚分离完成，不算占用
			}
			for _, p := range paths {
				if p == src {
					mountedBy = append(mountedBy, vmName)
					break
				}
			}
		}
	}

	if reason := detachVolKeepReason(src, managedImages, backingChildren, mountedBy); reason != "" {
		LogError(c, fmt.Errorf("分离磁盘后保留卷（未删）vm=%s vol=%s 原因=%s", vm.Name, filepath.Base(src), reason))
		resp["keep_reason"] = reason
		Success(c, resp)
		return
	}

	// 删卷：池内卷走 libvirt vol-delete；libvirt 未识别为卷（seed 等直接落盘文件）
	// 或池外文件（显式指令）用 os 兜底删——与 tasks.execDeleteVM 的 tryDeleteVol 双保险一致。
	if ownerPool != "" {
		if err := h.Virt.DeleteVolume(ownerPool, filepath.Base(src)); err != nil {
			ErrorWithMessage(c, http.StatusInternalServerError, "磁盘已分离，但删除存储卷失败", err)
			return
		}
	}
	if _, err := os.Stat(src); err == nil {
		if err := os.Remove(src); err != nil && !os.IsNotExist(err) {
			ErrorWithMessage(c, http.StatusInternalServerError, "磁盘已分离，但删除卷文件失败", err)
			return
		}
	}

	resp["volume_deleted"] = true
	Success(c, resp)
}

// AttachInterface 添加网卡（对应 virsh attach-interface，运行中生效并落配置）。
// body: {interface: InterfaceSpec}；mac 为空自动生成。
func (h *VMHandler) AttachInterface(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
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
		mac, err := virt.RandomMAC()
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
	mac := c.Param("mac")
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	if err := h.Virt.DetachInterface(vm.Name, mac); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "mac": mac})
}
