package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/service/setting"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// SettingsHandler 系统设置处理器：GET 运维可见的生效配置快照，PUT 修改可写配置项。
type SettingsHandler struct {
	DB       *gorm.DB
	Virt     *virt.Virt
	Settings *setting.Manager
}

// NewSettingsHandler 创建系统设置处理器。
func NewSettingsHandler(db *gorm.DB, mgr *setting.Manager) *SettingsHandler {
	return &SettingsHandler{DB: db, Virt: virt.New(), Settings: mgr}
}

// GetSettings 返回平台生效配置快照（仅管理员）。
// 前端"系统设置"页分组展示；轮询偏好等纯前端配置由浏览器 localStorage 维护，不经过后端。
// tasks.worker/queue 与 sessions.vnc_stale_min 读真实生效值（含 DB 可写配置覆盖）。
func (h *SettingsHandler) GetSettings(c *gin.Context) {
	cfg := config.GlobalConfig

	pools, _ := h.Virt.ListPools()
	networks, _ := h.Virt.ListNetworks()
	netNames := make([]string, 0, len(networks))
	for _, n := range networks {
		netNames = append(netNames, n.Name)
	}

	Success(c, gin.H{
		"platform": gin.H{
			"version":     "1.0.0",
			"server_mode": cfg.ServerMode,
			"server_port": cfg.ServerPort,
			"time":        time.Now().Format("2006-01-02 15:04:05"),
		},
		"virt": gin.H{
			"libvirt_uri": virt.URI,
		},
		"storage": gin.H{
			"image_dir": cfg.ImageDir,
			"seed_dir":  cfg.SeedDir,
			"pools":     pools,
		},
		"network": gin.H{
			"networks": netNames,
		},
		"tasks": gin.H{
			"workers":      tasks.WorkerCount,
			"queue_buffer": tasks.QueueBufferSize,
		},
		"sessions": gin.H{
			"vnc_stale_min": h.Settings.All()[setting.KeyVNCStaleMin],
		},
		// 可写配置项当前值（未设置过的键为默认值），设置页表单回填用
		"writable": h.Settings.All(),
	})
}

// UpdateSettings 修改可写配置项（仅管理员）。请求体字段全部可选，只更新出现的键。
// 白名单与取值范围由 service/setting.Validate 把关；写入即生效（消费方每次实时读取）。
func (h *SettingsHandler) UpdateSettings(c *gin.Context) {
	var req struct {
		DefaultStoragePool *string `json:"default_storage_pool"`
		VNCTokenTTLMin     *int    `json:"vnc_token_ttl_min"`
		VNCStaleMin        *int    `json:"vnc_stale_min"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}

	updated := make([]string, 0, 3)
	if req.DefaultStoragePool != nil {
		if err := h.Settings.Set(setting.KeyDefaultStoragePool, *req.DefaultStoragePool); err != nil {
			Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		updated = append(updated, "默认存储池")
	}
	if req.VNCTokenTTLMin != nil {
		if err := h.Settings.Set(setting.KeyVNCTokenTTLMin, strconv.Itoa(*req.VNCTokenTTLMin)); err != nil {
			Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		updated = append(updated, "VNC token 有效期")
	}
	if req.VNCStaleMin != nil {
		if err := h.Settings.Set(setting.KeyVNCStaleMin, strconv.Itoa(*req.VNCStaleMin)); err != nil {
			Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		updated = append(updated, "VNC 会话过期判定时长")
	}
	if len(updated) == 0 {
		Fail(c, http.StatusBadRequest, "没有需要更新的配置项")
		return
	}

	Success(c, gin.H{
		"message":  "已更新: " + strings.Join(updated, "、"),
		"writable": h.Settings.All(),
	})
}
