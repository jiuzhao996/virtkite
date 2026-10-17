package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
)

// ScanImportVMs 扫描宿主机上未被平台纳管的存量域（virsh 已定义、DB 无记录的 VM）。
// 返回全部候选域及其硬件摘要，前端据此勾选导入；libvirt 侧不做任何改动。
// 只读操作：扫描失败/单盘取大小失败都不影响已拿到的域列表（DiskGB 失败为 0）。
func (h *VMHandler) ScanImportVMs(c *gin.Context) {
	// 单机部署：扫描目标固定为平台登记的首台宿主机
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

	// 已纳管集合包含软删除记录：软删的 VM 仍占用同名/同 UUID 域，
	// 若不排除，用户会反复看到「可导入」但导入时又被跳过（或被唯一索引拒绝）。
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
		// 磁盘大小按需探测，失败返回 0 不影响导入流程（只是前端少显示一个数字）
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
// 采用「部分成功」语义：单台失败不影响其余，最终返回 imported/skipped/failed 与
// errors 明细，前端逐条展示（ImportVMs errors 数组已消费，见 AGENTS.md 遗留清单）。
func (h *VMHandler) ImportVMs(c *gin.Context) {
	var req struct {
		HostID uint     `json:"host_id"`
		Names  []string `json:"names"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Names) == 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "请选择要导入的虚拟机", err)
		return
	}

	// host_id 省略时回退到首台宿主机，与创建向导的单机语义一致
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

	// 重新扫描而不是信任前端传来的硬件字段：导入数据必须来自 libvirt 实时状态，
	// 否则前端可以伪造 vcpu/内存/磁盘大小写进 DB。
	details, err := h.Virt.ListDomainsWithDetail()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "扫描宿主机虚拟机失败", err)
		return
	}
	byName := make(map[string]virt.DomainDetail, len(details))
	for _, d := range details {
		byName[d.Name] = d
	}

	// 池路径映射失败不阻断导入：只是推断不出 storage_pool（留空），
	// 后续删卷守卫会因「池外文件」而保守保留卷。
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

		// 按 UUID 去重（含软删除）：UUID 才是 libvirt 域的身份，重名域在 libvirt 侧本就不允许
		var cnt int64
		h.DB.Unscoped().Model(&model.VM{}).Where("uuid = ?", d.UUID).Count(&cnt)
		if cnt > 0 {
			skipped++
			continue
		}

		// 由磁盘路径反推所属存储池：路径以池目标路径 + "/" 开头即归属该池。
		// 推断不出就留空——比瞎猜一个池更安全（删卷守卫会因此保留卷）。
		pool := ""
		for p, path := range poolPaths {
			if d.DiskPath != "" && strings.HasPrefix(d.DiskPath, path+"/") {
				pool = p
				break
			}
		}

		// 只写 DB 记录：不 define/undefine、不碰磁盘，因此导入失败也不会影响存量 VM。
		// Status 直接取 libvirt 实时状态（已过 StateToPlatform 映射，只会是
		// running / shut off / paused / error）。
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
// 单机部署约定：未显式指定 host_id 时一律落到 id 最小的那台；
// 多宿主机能力尚未实现（见 AGENTS.md 遗留清单），调用方不要据此假设可水平扩展。
func (h *VMHandler) firstHost() (*model.Host, error) {
	var host model.Host
	if err := h.DB.Order("id ASC").First(&host).Error; err != nil {
		return nil, err
	}
	return &host, nil
}

// trackedUUIDs 返回 DB 中已纳管（含软删除）的全部 VM UUID 集合。
// Unscoped 是刻意的：软删除的 VM 仍可能对应宿主上存活的域，
// 若不算作已纳管，扫描页会把它们重新标为「可导入」，造成反复误操作。
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
// 所有子项均 best-effort：某个 libvirt 查询失败只让对应列表为空，
// 不整页 500——向导至少还能让用户看到其余可选项与明确错误。
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
	// 这里返回全部镜像而非只返回 template：建机向导也支持直接用 ISO/普通镜像安装。
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
		// 平台配置的默认存储池（系统设置 default_storage_pool）：建机向导据此预选，
		// 尊重用户设定，而非让前端按路径猜（此前向导忽略该设置、默认落到首个池）。
		"default_storage_pool": tasks.DefaultStoragePoolResolver(),
	})
}

// TestInstallSource 测试网络安装源可达性（建机向导「测试」按钮）：POST /api/vms/install-source/test。
// body: {url}。只读探测——复用下载的相对路径抓首字节，不落盘、不建机。
// 探测失败不返回 5xx：这是「测试」而非「执行」，用 200 + available=false 让前端
// 以普通提示展示原因，避免浏览器控制台出现红色网络错误。
func (h *VMHandler) TestInstallSource(c *gin.Context) {
	var req struct {
		URL string `json:"url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.URL) == "" {
		Fail(c, http.StatusBadRequest, "请提供网络安装源地址")
		return
	}
	if err := tasks.ProbeInstallSource(req.URL); err != nil {
		// 完整错误（含 curl stderr）只进日志；前端只收中文友好消息（AGENTS 后端标准 #3）
		LogError(c, err)
		Success(c, gin.H{"available": false, "message": "安装源不可用：地址不可达、路径不对，或不是 RHEL 系（Rocky/CentOS 等）安装树布局"})
		return
	}
	Success(c, gin.H{"available": true, "message": "安装树可达：已能取到内核与 initrd"})
}

// poolPathMap 返回 存储池名 → 目标路径 的映射（供磁盘归属推断）。
// 单个池取路径失败只跳过该池（不影响其余映射）；调用方对整体错误也按
// 「推断不出池」降级处理，不会因存储池查询失败阻断导入。
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
