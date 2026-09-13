package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

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
			// 完整错误（含 GORM/SQL 原文）只进服务端日志，响应里只给中文原因 + 域名，
			// 避免把表结构、约束名等内部细节泄漏到前端（见 AGENTS.md 后端标准第 3 条）
			LogError(c, fmt.Errorf("导入存量虚拟机 %s 写入数据库失败: %w", name, err))
			errs = append(errs, fmt.Sprintf("%s: 写入数据库失败", name))
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
