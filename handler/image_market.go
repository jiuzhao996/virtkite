// image_market.go：云镜像市场（v3 批次 H）——内置官方云镜像清单 + 下载任务提交入口。
//
// GET  /api/images/market          返回清单，每项标注默认下载目标池
// POST /api/images/market/download 仅 admin；校验后 Submit("image_download") 异步下载，202 返回 task_id
//
// 下载执行（流式落盘 + images 表登记）在 service/tasks/image_download.go 的
// image_download executor 内完成，本文件只做目录维护与入口校验，不碰文件系统。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// marketItem 云镜像市场清单项（内置硬编码，key 是下载接口的唯一入参）。
type marketItem struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	OSName   string `json:"os_name"` // 须与 virt.OSList 的 Name 精确一致，前端才能按镜像自动选中 OS
	URL      string `json:"url"`
	SizeHint int64  `json:"size_hint"` // 当前实测约值（字节），「latest」类 URL 会随上游小版本浮动
	Official string `json:"official"`  // 官方文档/下载页
	Desc     string `json:"description"`
}

// marketCatalog 内置官方云镜像清单（URL 均已 HEAD 实测 200）。
// os_name 与 virt.OSList 的对齐说明：
//   - AlmaLinux 9：OSList 暂无该条目，先如实标注（自动识别落空由用户手选，待 OSList 补条目闭环）；
//   - Fedora：OSList 只有 "Fedora 40" 一个 Fedora 项（virtio 设备模型跨版本一致），
//     镜像给最新稳定版（44），os_name 标到最近可选项。
var marketCatalog = []marketItem{
	{
		Key:      "ubuntu-24.04",
		Name:     "Ubuntu 24.04 LTS",
		OSName:   "Ubuntu 24.04 LTS",
		URL:      "https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-amd64.img",
		SizeHint: 625256960,
		Official: "https://cloud-images.ubuntu.com/releases/24.04/release/",
		Desc:     "Ubuntu Server 24.04 LTS 官方云镜像（qcow2，预装 cloud-init）",
	},
	{
		Key:      "ubuntu-22.04",
		Name:     "Ubuntu 22.04 LTS",
		OSName:   "Ubuntu 22.04 LTS",
		URL:      "https://cloud-images.ubuntu.com/releases/22.04/release/ubuntu-22.04-server-cloudimg-amd64.img",
		SizeHint: 735388672,
		Official: "https://cloud-images.ubuntu.com/releases/22.04/release/",
		Desc:     "Ubuntu Server 22.04 LTS 官方云镜像（qcow2，预装 cloud-init）",
	},
	{
		Key:      "rocky-9",
		Name:     "Rocky Linux 9",
		OSName:   "Rocky Linux 9",
		URL:      "https://download.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base.latest.x86_64.qcow2",
		SizeHint: 645988352,
		Official: "https://wiki.rockylinux.org/",
		Desc:     "Rocky Linux 9 GenericCloud 官方云镜像（qcow2，预装 cloud-init）",
	},
	{
		Key:      "debian-12",
		Name:     "Debian 12",
		OSName:   "Debian 12",
		URL:      "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-generic-amd64.qcow2",
		SizeHint: 449314816,
		Official: "https://cloud.debian.org/images/cloud/bookworm/",
		Desc:     "Debian 12 (bookworm) generic 官方云镜像（qcow2，预装 cloud-init）",
	},
	{
		Key:      "almalinux-9",
		Name:     "AlmaLinux 9",
		OSName:   "AlmaLinux 9",
		URL:      "https://repo.almalinux.org/almalinux/9/cloud/x86_64/images/AlmaLinux-9-GenericCloud-latest.x86_64.qcow2",
		SizeHint: 589299712,
		Official: "https://wiki.almalinux.org/cloud/",
		Desc:     "AlmaLinux 9 GenericCloud 官方云镜像（qcow2，预装 cloud-init）",
	},
	{
		Key:      "fedora-cloud",
		Name:     "Fedora Cloud 44",
		OSName:   "Fedora 40",
		URL:      "https://download.fedoraproject.org/pub/fedora/linux/releases/44/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2",
		SizeHint: 583729152,
		Official: "https://alt.fedoraproject.org/cloud/",
		Desc:     "Fedora Cloud Base 44 官方云镜像（qcow2，预装 cloud-init）",
	},
}

// ImageMarketHandler 云镜像市场处理器。
type ImageMarketHandler struct {
	DB    *gorm.DB
	Tasks *tasks.Manager
	Virt  *virt.Virt
}

// NewImageMarketHandler 创建云镜像市场处理器。
func NewImageMarketHandler(db *gorm.DB, taskMgr *tasks.Manager) *ImageMarketHandler {
	return &ImageMarketHandler{DB: db, Virt: virt.New(), Tasks: taskMgr}
}

// marketItemByKey 按 key 查清单（6 项线性扫即可，无需索引）。
func marketItemByKey(key string) (marketItem, bool) {
	for _, it := range marketCatalog {
		if it.Key == key {
			return it, true
		}
	}
	return marketItem{}, false
}

// ListMarket 返回内置清单，每项标注默认下载目标池（系统设置 default_storage_pool）。
// 池仅是「默认值」标注：真正落哪个池以下载请求里的 pool 为准，下载时点再校验存在性。
func (h *ImageMarketHandler) ListMarket(c *gin.Context) {
	pool := tasks.DefaultStoragePoolResolver()
	items := make([]gin.H, 0, len(marketCatalog))
	for _, it := range marketCatalog {
		fileName, _ := tasks.FileNameFromURL(it.URL)
		items = append(items, gin.H{
			"key":         it.Key,
			"name":        it.Name,
			"os_name":     it.OSName,
			"url":         it.URL,
			"file_name":   fileName,
			"size_hint":   it.SizeHint,
			"official":    it.Official,
			"description": it.Desc,
			"pool":        pool,
		})
	}
	Success(c, gin.H{
		"total": len(items),
		"pool":  pool,
		"items": items,
	})
}

// Download 提交云镜像下载任务（仅 admin：images 组是 OperatorMiddleware，operator 可达，
// 故 handler 内二次收口）。body: {key*, pool?}；pool 缺省走系统设置 default_storage_pool，
// 不存在的池在任务提交前 400 挡住（免得任务起跑后才失败）。
func (h *ImageMarketHandler) Download(c *gin.Context) {
	// 不用 requireAdminRole：其 403 文案是「授权管理仅管理员可用」，语境不符；角色闸语义相同
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "云镜像下载仅管理员可用")
		return
	}
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	var req struct {
		Key  string `json:"key" binding:"required"`
		Pool string `json:"pool"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	item, ok := marketItemByKey(req.Key)
	if !ok {
		Fail(c, http.StatusNotFound, "云镜像市场中不存在："+req.Key)
		return
	}
	pool := req.Pool
	if pool == "" {
		pool = tasks.DefaultStoragePoolResolver()
	}
	if _, err := h.Virt.GetPoolPath(pool); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "存储池 "+pool+" 不存在或不可用", err)
		return
	}
	payload := map[string]interface{}{
		"url":  item.URL,
		"name": item.Name,
		"pool": pool,
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("image_download", "下载云镜像 "+item.Name, payload, userID, username, "", nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交下载任务失败", err)
		return
	}
	Accepted(c, "下载任务已提交", gin.H{"task_id": task.ID})
}
