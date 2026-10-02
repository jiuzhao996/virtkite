// image_market_iso.go：官方安装 ISO 下载清单（云镜像之外的另一形态——传统光盘安装介质）。
//
// 与云镜像（qcow2，预装 cloud-init，分钟级出机）的分工：
//   - 云镜像 → 创建向导「云镜像」方式，免安装流程；
//   - 安装 ISO → 创建向导「本地安装介质 (ISO)」方式，走传统装机（教学演示装机过程用）。
//
// GET  /api/images/market/iso          返回 ISO 清单
// POST /api/images/market/iso/download 仅 admin；复用 image_download executor（流式落盘 + 登记 iso 卷）
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/tasks"
)

// isoMarketItem 官方安装 ISO 清单项。单源直下（2026-10 砍掉双源），每项来源如实标注
// Source（清华源/南大源），MirrorPage 是源站目录页（前端「访问源页面」链接）。
// 全部 URL 2026-10-02 真下载首字节复验：ISO 魔数 CD001 ✓，size_hint 取当日 Content-Length。
type isoMarketItem struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	SizeHint   int64  `json:"size_hint"`
	Source     string `json:"source_label"` // 来源标识（前端徽标直出）
	MirrorPage string `json:"mirror_page"`  // 源站目录页（前端「访问源页面」链接）
	Desc       string `json:"description"`
}

// isoMarketCatalog 内置官方安装 ISO 清单（镜像市场 · ISO 子页消费）。
// Rocky 未收录：清华未镜像 rocky（tunasync 清单实测），保留南大源并如实标注。
var isoMarketCatalog = []isoMarketItem{
	{
		Key: "ubuntu-26.04-server-iso", Name: "Ubuntu 26.04 LTS Server",
		URL:        "https://mirrors.tuna.tsinghua.edu.cn/ubuntu-releases/26.04/ubuntu-26.04.1-live-server-amd64.iso",
		SizeHint:   2927861760,
		Source:     "清华源",
		MirrorPage: "https://mirrors.tuna.tsinghua.edu.cn/ubuntu-releases/26.04/",
		Desc:       "Ubuntu Server 26.04.1 live-server 安装镜像（清华源，最新 LTS）",
	},
	{
		Key: "ubuntu-24.04-server-iso", Name: "Ubuntu 24.04 LTS Server",
		URL:        "https://mirrors.tuna.tsinghua.edu.cn/ubuntu-releases/24.04/ubuntu-24.04.3-live-server-amd64.iso",
		SizeHint:   3303444480,
		Source:     "清华源",
		MirrorPage: "https://mirrors.tuna.tsinghua.edu.cn/ubuntu-releases/24.04/",
		Desc:       "Ubuntu Server 24.04.3 live-server 安装镜像（清华源）",
	},
	{
		Key: "rocky-10-dvd", Name: "Rocky Linux 10",
		URL:        "https://mirror.nju.edu.cn/rocky/10/isos/x86_64/Rocky-10-latest-x86_64-dvd.iso",
		SizeHint:   10226171904,
		Source:     "南大源",
		MirrorPage: "https://mirror.nju.edu.cn/rocky/10/isos/x86_64/",
		Desc:       "Rocky Linux 10 DVD 完整安装镜像（含全部软件包组；清华未收录 rocky）",
	},
	{
		Key: "fedora-44-server-iso", Name: "Fedora Server 44",
		URL:        "https://mirrors.tuna.tsinghua.edu.cn/fedora/releases/44/Server/x86_64/iso/Fedora-Server-dvd-x86_64-44-1.7.iso",
		SizeHint:   3913023488,
		Source:     "清华源",
		MirrorPage: "https://mirrors.tuna.tsinghua.edu.cn/fedora/releases/44/Server/x86_64/iso/",
		Desc:       "Fedora Server 44 DVD 安装镜像（清华源）",
	},
	{
		Key: "debian-13-netinst", Name: "Debian 13",
		URL:        "https://mirrors.tuna.tsinghua.edu.cn/debian-cd/13.7.0/amd64/iso-cd/debian-13.7.0-amd64-netinst.iso",
		SizeHint:   792723456,
		Source:     "清华源",
		MirrorPage: "https://mirrors.tuna.tsinghua.edu.cn/debian-cd/13.7.0/amd64/iso-cd/",
		Desc:       "Debian 13 netinst 网络安装镜像（小体积，装时拉包；清华源）",
	},
	{
		Key: "opensuse-16-offline", Name: "openSUSE Leap 16",
		URL:        "https://mirrors.tuna.tsinghua.edu.cn/opensuse/distribution/leap/16.0/iso/Leap-16.0-offline-installer-x86_64.install.iso",
		SizeHint:   4538236928,
		Source:     "清华源",
		MirrorPage: "https://mirrors.tuna.tsinghua.edu.cn/opensuse/distribution/leap/16.0/iso/",
		Desc:       "openSUSE Leap 16 离线安装镜像（含全部安装源；清华源）",
	},
}

// isoItemByKey 按 key 查 ISO 清单。
func isoItemByKey(key string) (isoMarketItem, bool) {
	for _, it := range isoMarketCatalog {
		if it.Key == key {
			return it, true
		}
	}
	return isoMarketItem{}, false
}

// ListMarketISO 返回官方安装 ISO 清单（标注默认下载池）。
func (h *ImageMarketHandler) ListMarketISO(c *gin.Context) {
	pool := tasks.DefaultStoragePoolResolver()
	items := make([]gin.H, 0, len(isoMarketCatalog))
	for _, it := range isoMarketCatalog {
		fileName, _ := tasks.FileNameFromURL(it.URL)
		items = append(items, gin.H{
			"key":          it.Key,
			"name":         it.Name,
			"url":          it.URL,
			"file_name":    fileName,
			"size_hint":    it.SizeHint,
			"source_label": it.Source,
			"mirror_page":  it.MirrorPage,
			"description":  it.Desc,
			"pool":         pool,
		})
	}
	Success(c, gin.H{"total": len(items), "pool": pool, "items": items})
}

// DownloadISO 提交官方安装 ISO 下载任务（仅 admin，复用 image_download executor）。
// body: {key*, pool?}
func (h *ImageMarketHandler) DownloadISO(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "安装镜像下载仅管理员可用")
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
	item, ok := isoItemByKey(req.Key)
	if !ok {
		Fail(c, http.StatusNotFound, "安装镜像清单中不存在："+req.Key)
		return
	}
	pool := req.Pool
	if pool == "" {
		pool = tasks.DefaultStoragePoolResolver()
	}
	payload := map[string]interface{}{
		"url":  item.URL,
		"name": item.Name,
		"pool": pool,
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("image_download", "下载安装镜像 "+item.Name, payload, userID, username, "", nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交下载任务失败", err)
		return
	}
	Accepted(c, "下载任务已提交", gin.H{"task_id": task.ID})
}
