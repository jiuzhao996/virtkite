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
	OSName   string `json:"os_name"`  // 须与 virt.OSList 的 Name 精确一致，前端才能按镜像自动选中 OS
	URL      string `json:"url"`      // 国内镜像源（默认下载走国内，分钟级 vs 官方源小时级）
	SizeHint int64  `json:"size_hint"`
	Official string `json:"official"` // 官方文档/下载页
	CNURL    string `json:"-"`        // 官方源（可选；下载时用户可选官方/国内，双源互为备份）
	Desc     string `json:"description"`
}

// marketCatalog 内置官方云镜像清单（URL 均已 HEAD 实测 200，2026-09-29 批次扩容复测）。
// os_name 与 virt.OSList 的对齐说明：
//   - AlmaLinux 9/10、openSUSE Leap 16、Arch Linux：OSList 已同步补条目（本批次闭环）；
//   - Fedora：OSList 只有 "Fedora 40" 一个 Fedora 项（virtio 设备模型跨版本一致），
//     镜像给最新稳定版（44），os_name 标到最近可选项；
//   - 银河麒麟/openKylin：官方云镜像直链需注册或网络不可达（HEAD 实测），暂不入市场；
//     可经 ISO 安装镜像方式安装（OSList 已有 Kylin V10 条目）；
//   - openSUSE：download.opensuse.org 对国内 IP 有 Cerberus 反爬挑战——HEAD 200 但 GET
//     落盘的是 5KB HTML 假镜像（真实下载实证），不能只信 HEAD，暂不入市场。
// marketCatalog 内置官方云镜像清单（只留最新稳定版；下载时可选国内源/官方源双源）。
// 统一国内源：南京大学 mirror.nju.edu.cn（全 6 项 HEAD 200 实测 2026-09-29，国内下载分钟级）；
// Arch 用清华源（唯一国内 Arch 云镜像）。官方源保留在 CNURL 作备选字段（UI 不再展示选择器）
// os_name 与 virt.OSList 对齐：Alma 9/10、openSUSE 16 已补条目；Fedora 标最近可选项；
// 银河麒麟/openKylin 云镜像直链需注册（HEAD 不可达）不入市场，走 ISO 安装（OSList 已有）。
var marketCatalog = []marketItem{
	{
		Key: "ubuntu-26.04", Name: "Ubuntu 26.04 LTS", OSName: "Ubuntu 26.04 LTS",
		URL:      "https://mirror.nju.edu.cn/ubuntu-cloud-images/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64.img",
		SizeHint: 625256960,
		Official: "https://cloud-images.ubuntu.com/releases/26.04/release/",
		CNURL:    "https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64.img",
		Desc:     "Ubuntu Server 26.04 LTS 官方云镜像（qcow2，预装 cloud-init）",
	},
	{
		Key: "debian-13", Name: "Debian 13", OSName: "Debian 13",
		URL:      "https://mirror.nju.edu.cn/debian-cdimage/cloud/trixie/latest/debian-13-generic-amd64.qcow2",
		SizeHint: 449314816,
		Official: "https://cloud.debian.org/images/cloud/trixie/",
		CNURL:    "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.qcow2",
		Desc:     "Debian 13 (trixie) generic 官方云镜像（qcow2，预装 cloud-init）",
	},
	{
		Key: "rocky-10", Name: "Rocky Linux 10", OSName: "Rocky Linux 10",
		URL:      "https://mirror.nju.edu.cn/rocky/10/images/x86_64/Rocky-10-GenericCloud-Base.latest.x86_64.qcow2",
		SizeHint: 645988352,
		Official: "https://download.rockylinux.org/pub/rocky/10/images/x86_64/Rocky-10-GenericCloud-Base.latest.x86_64.qcow2",
		CNURL:    "https://download.rockylinux.org/pub/rocky/10/images/x86_64/Rocky-10-GenericCloud-Base.latest.x86_64.qcow2",
		Desc:     "Rocky Linux 10 GenericCloud 官方云镜像（qcow2，预装 cloud-init）",
	},
	{
		Key: "fedora-cloud", Name: "Fedora Cloud 44", OSName: "Fedora 40",
		URL:      "https://mirror.nju.edu.cn/fedora/releases/44/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2",
		SizeHint: 583729152,
		Official: "https://alt.fedoraproject.org/cloud/",
		CNURL:    "https://download.fedoraproject.org/pub/fedora/linux/releases/44/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2",
		Desc:     "Fedora Cloud Base 44 官方云镜像（qcow2，预装 cloud-init；南大源）",
	},
	{
		Key: "archlinux", Name: "Arch Linux", OSName: "Arch Linux",
		URL:      "https://mirrors.tuna.tsinghua.edu.cn/archlinux/images/latest/Arch-Linux-x86_64-cloudimg.qcow2",
		SizeHint: 314572800,
		Official: "https://geo.mirror.rackspace.com/archlinux/images/latest/",
		CNURL:    "https://geo.mirror.rackspace.com/archlinux/images/latest/Arch-Linux-x86_64-cloudimg.qcow2",
		Desc:     "Arch Linux 官方云镜像（qcow2，预装 cloud-init，滚动更新；清华源）",
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
			"cn_url":      it.CNURL, // 官方源备选（前端「下载时可选官方/国内」）
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
		Key    string `json:"key" binding:"required"`
		Pool   string `json:"pool"`
		Source string `json:"source"` // cn=国内镜像源（默认）| official=官方源
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
	// 双源：URL=国内镜像源（默认），CNURL=官方源。用户下载时可选，互为备份
	downloadURL := item.URL
	if req.Source == "official" && item.CNURL != "" {
		downloadURL = item.CNURL
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
		"url":  downloadURL,
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
