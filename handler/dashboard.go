package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// DashboardHandler 仪表盘统计处理器
type DashboardHandler struct {
	DB *gorm.DB
}

// NewDashboardHandler 创建仪表盘处理器
func NewDashboardHandler(db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{DB: db}
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

	Success(c, gin.H{
		"host_count":      hostCount,
		"vm_count":        vmCount,
		"running_vm_count": runningVMCount,
		"image_count":     imageCount,
		"user_count":      userCount,
		"audit_count":     auditCount,
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
