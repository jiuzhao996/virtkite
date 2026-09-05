package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// SettingsHandler 系统设置处理器（运维可见的生效配置快照，只读）。
type SettingsHandler struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewSettingsHandler 创建系统设置处理器。
func NewSettingsHandler(db *gorm.DB) *SettingsHandler {
	return &SettingsHandler{DB: db, Virt: virt.New()}
}

// GetSettings 返回平台生效配置快照（仅管理员）。
// 前端"系统设置"页分组展示；轮询偏好等纯前端配置由浏览器 localStorage 维护，不经过后端。
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
			"libvirt_uri": cfg.LibvirtURI,
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
			"workers":      4,
			"queue_buffer": 128,
		},
		"sessions": gin.H{
			"vnc_stale_min": 60,
		},
	})
}
