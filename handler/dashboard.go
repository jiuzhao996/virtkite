package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// DashboardHandler 仪表盘统计处理器
type DashboardHandler struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewDashboardHandler 创建仪表盘处理器
func NewDashboardHandler(db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{DB: db, Virt: virt.New()}
}

// Overview 平台总览统计
func (h *DashboardHandler) Overview(c *gin.Context) {
	var hostCount, vmCount, runningVMCount, imageCount, userCount, auditCount int64

	h.DB.Model(&model.Host{}).Count(&hostCount)
	h.DB.Model(&model.VM{}).Count(&vmCount)
	h.DB.Model(&model.VM{}).Where("status = ?", "running").Count(&runningVMCount)
	h.DB.Model(&model.Image{}).Count(&imageCount)
	h.DB.Model(&model.User{}).Count(&userCount)
	h.DB.Model(&model.AuditLog{}).Count(&auditCount)

	// 存储池 / 网络计数（实时从 libvirt 获取，失败则置 0）
	poolCount := 0
	networkCount := 0
	if pools, err := h.Virt.ListPools(); err == nil {
		poolCount = len(pools)
	}
	if nets, err := h.Virt.ListNetworks(); err == nil {
		networkCount = len(nets)
	}

	Success(c, gin.H{
		"host_count":       hostCount,
		"vm_count":         vmCount,
		"running_vm_count": runningVMCount,
		"image_count":      imageCount,
		"user_count":       userCount,
		"audit_count":      auditCount,
		"pool_count":       poolCount,
		"network_count":    networkCount,
	})
}

// VMStatusDistribution 虚拟机状态分布
func (h *DashboardHandler) VMStatusDistribution(c *gin.Context) {
	type statusCount struct {
		Status string `json:"status"`
		Count  int64  `json:"count"`
	}

	var result []statusCount
	h.DB.Model(&model.VM{}).
		Select("status, count(*) as count").
		Group("status").
		Scan(&result)

	Success(c, result)
}