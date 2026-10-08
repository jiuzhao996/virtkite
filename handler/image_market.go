// image_market.go：云镜像市场（v3 批次 H）——内置云镜像清单 + 下载任务提交入口。
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
// 单源直下（2026-10 用户拍板砍掉双源选择器）：每项只有一个 URL，来源如实标注在
// Source（清华源/南大源），MirrorPage 是源站目录页（前端「访问源页面」链接）。
type marketItem struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	OSName     string `json:"os_name"` // 须与 virt.OSList 的 Name 精确一致，前端才能按镜像自动选中 OS
	URL        string `json:"url"`     // 唯一下载直链
	SizeHint   int64  `json:"size_hint"`
	Source     string `json:"source_label"` // 来源标识（前端徽标直出：清华源/南大源）
	MirrorPage string `json:"mirror_page"`  // 源站目录页（前端「访问源页面」链接）
	Desc       string `json:"description"`
}

// marketCatalog 内置云镜像清单（全部 URL 2026-10-02 真下载首字节复验：qcow2 魔数 QFI ✓）。
//
// 源站选择规则（用户拍板「来自清华源」）：清华有的走清华；清华未收录的（ubuntu-cloud-images
// 的 releases 文件、debian-cdimage、rocky）保留南大源并在卡片上如实标注「南大源」。
// os_name 与 virt.OSList 对齐：Fedora 标最近可选项（OSList 只有 Fedora 40 一档）。
// CentOS Stream 未收录：清华/南大都只有 yum 仓库无安装介质/云镜像（iso、images 目录 404
// 实测），官方 cloud.centos.org 亦 502 不可达，无可用下载源故不入市场。
var marketCatalog = []marketItem{
	{
		Key: "ubuntu-26.04", Name: "Ubuntu 26.04 LTS", OSName: "Ubuntu 26.04 LTS",
		URL:        "https://mirror.nju.edu.cn/ubuntu-cloud-images/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64.img",
		SizeHint:   865115136,
		Source:     "南大源",
		MirrorPage: "https://mirror.nju.edu.cn/ubuntu-cloud-images/releases/26.04/release/",
		Desc:       "Ubuntu Server 26.04 LTS 官方云镜像（qcow2，预装 cloud-init；清华未收录该文件）",
	},
	{
		Key: "debian-13", Name: "Debian 13", OSName: "Debian 13",
		URL:        "https://mirror.nju.edu.cn/debian-cdimage/cloud/trixie/latest/debian-13-generic-amd64.qcow2",
		SizeHint:   433651712,
		Source:     "南大源",
		MirrorPage: "https://mirror.nju.edu.cn/debian-cdimage/cloud/trixie/latest/",
		Desc:       "Debian 13 (trixie) generic 官方云镜像（qcow2，预装 cloud-init；清华未收录 debian-cdimage）",
	},
	{
		Key: "rocky-10", Name: "Rocky Linux 10", OSName: "Rocky Linux 10",
		URL:        "https://mirror.nju.edu.cn/rocky/10/images/x86_64/Rocky-10-GenericCloud-Base.latest.x86_64.qcow2",
		SizeHint:   544997376,
		Source:     "南大源",
		MirrorPage: "https://mirror.nju.edu.cn/rocky/10/images/x86_64/",
		Desc:       "Rocky Linux 10 GenericCloud 官方云镜像（qcow2，预装 cloud-init；清华未收录 rocky）",
	},
	{
		Key: "fedora-cloud", Name: "Fedora Cloud 44", OSName: "Fedora 40",
		URL:        "https://mirrors.tuna.tsinghua.edu.cn/fedora/releases/44/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2",
		SizeHint:   583729152,
		Source:     "清华源",
		MirrorPage: "https://mirrors.tuna.tsinghua.edu.cn/fedora/releases/44/Cloud/x86_64/images/",
		Desc:       "Fedora Cloud Base 44 官方云镜像（qcow2，预装 cloud-init；清华源）",
	},
	{
		Key: "archlinux", Name: "Arch Linux", OSName: "Arch Linux",
		URL:        "https://mirrors.tuna.tsinghua.edu.cn/archlinux/images/latest/Arch-Linux-x86_64-cloudimg.qcow2",
		SizeHint:   578080256,
		Source:     "清华源",
		MirrorPage: "https://mirrors.tuna.tsinghua.edu.cn/archlinux/images/latest/",
		Desc:       "Arch Linux 官方云镜像（qcow2，预装 cloud-init，滚动更新；清华源）",
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

// marketItemByKey 按 key 查清单（5 项线性扫即可，无需索引）。
func marketItemByKey(key string) (marketItem, bool) {
	for _, it := range marketCatalog {
		if it.Key == key {
			return it, true
		}
	}
	return marketItem{}, false
}

// ListMarket 返回内置清单，每项标注默认下载目标池（镜像库池 default）。
// 池仅是「默认值」标注：真正落哪个池以下载请求里的 pool 为准，下载时点再校验存在性。
func (h *ImageMarketHandler) ListMarket(c *gin.Context) {
	pool := imagePool
	items := make([]gin.H, 0, len(marketCatalog))
	for _, it := range marketCatalog {
		fileName, _ := tasks.FileNameFromURL(it.URL)
		items = append(items, gin.H{
			"key":          it.Key,
			"name":         it.Name,
			"os_name":      it.OSName,
			"url":          it.URL,
			"file_name":    fileName,
			"size_hint":    it.SizeHint,
			"source_label": it.Source,
			"mirror_page":  it.MirrorPage,
			"description":  it.Desc,
			"pool":         pool,
		})
	}
	Success(c, gin.H{
		"total": len(items),
		"pool":  pool,
		"items": items,
	})
}

// Download 提交云镜像下载任务（仅 admin：images 组是 OperatorMiddleware，operator 可达，
// 故 handler 内二次收口）。body: {key*, pool?}；pool 缺省落镜像库池 default，
// 不存在的池在任务提交前 400 挡住（免得任务起跑后才失败）。
// 单源直下（2026-10 砍掉 cn/official 双源参数）：URL 即清单标注的唯一来源。
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
		pool = imagePool
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
