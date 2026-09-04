package handler

import (
	"net/http"
	"os/exec"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// HostHandler 宿主机处理器
type HostHandler struct {
	DB *gorm.DB
}

// NewHostHandler 创建宿主机处理器
func NewHostHandler(db *gorm.DB) *HostHandler {
	return &HostHandler{DB: db}
}

// ListHosts 获取宿主机列表
func (h *HostHandler) ListHosts(c *gin.Context) {
	var hosts []model.Host
	if err := h.DB.Find(&hosts).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询宿主机失败", err)
		return
	}

	Success(c, gin.H{
		"total": len(hosts),
		"items": hosts,
	})
}

// CreateHost 添加宿主机
func (h *HostHandler) CreateHost(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		LibvirtURI  string `json:"libvirt_uri"`
		SSHIP       string `json:"ssh_ip" binding:"required"`
		SSHPort     int    `json:"ssh_port"`
		SSHUser     string `json:"ssh_user"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	// 设置默认值
	if req.LibvirtURI == "" {
		req.LibvirtURI = "qemu:///system"
	}
	if req.SSHPort == 0 {
		req.SSHPort = 22
	}
	if req.SSHUser == "" {
		req.SSHUser = "root"
	}

	// 创建宿主机
	host := model.Host{
		Name:        req.Name,
		LibvirtURI:  req.LibvirtURI,
		SSHIP:       req.SSHIP,
		SSHPort:     req.SSHPort,
		SSHUser:     req.SSHUser,
		Description: req.Description,
		Status:      "unknown",
	}

	if err := h.DB.Create(&host).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "添加宿主机失败", err)
		return
	}

	Created(c, "添加成功", host)
}

// UpdateHost 更新宿主机
func (h *HostHandler) UpdateHost(c *gin.Context) {
	id := c.Param("id")

	var host model.Host
	if err := h.DB.First(&host, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "宿主机不存在", err)
		return
	}

	var req struct {
		Name        *string `json:"name"`
		LibvirtURI  *string `json:"libvirt_uri"`
		SSHIP       *string `json:"ssh_ip"`
		SSHPort     *int    `json:"ssh_port"`
		SSHUser     *string `json:"ssh_user"`
		Description *string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	// 更新字段
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.LibvirtURI != nil {
		updates["libvirt_uri"] = *req.LibvirtURI
	}
	if req.SSHIP != nil {
		updates["ssh_ip"] = *req.SSHIP
	}
	if req.SSHPort != nil {
		updates["ssh_port"] = *req.SSHPort
	}
	if req.SSHUser != nil {
		updates["ssh_user"] = *req.SSHUser
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}

	if err := h.DB.Model(&host).Updates(updates).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "更新宿主机失败", err)
		return
	}

	Created(c, "更新成功", nil)
}

// DeleteHost 删除宿主机
func (h *HostHandler) DeleteHost(c *gin.Context) {
	id := c.Param("id")

	var host model.Host
	if err := h.DB.First(&host, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "宿主机不存在", err)
		return
	}

	// 检查是否有虚拟机关联
	var vmCount int64
	h.DB.Model(&model.VM{}).Where("host_id = ?", id).Count(&vmCount)
	if vmCount > 0 {
		Fail(c, http.StatusBadRequest, "宿主机下还有虚拟机，不能删除")
		return
	}

	if err := h.DB.Delete(&host).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "删除宿主机失败", err)
		return
	}

	Created(c, "删除成功", nil)
}

// TestHost 测试宿主机连通性
func (h *HostHandler) TestHost(c *gin.Context) {
	id := c.Param("id")

	var host model.Host
	if err := h.DB.First(&host, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "宿主机不存在", err)
		return
	}

	// 测试ping
	out, err := exec.Command("ping", "-c", "1", "-W", "2", host.SSHIP).Output()
	if err != nil {
		Success(c, gin.H{
			"reachable": false,
		})
		return
	}

	// 解析延迟
	latency := "0"
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.Contains(line, "time=") {
			parts := strings.Split(line, "time=")
			if len(parts) > 1 {
				latency = strings.Split(parts[1], " ")[0]
			}
		}
	}

	Success(c, gin.H{
		"reachable":  true,
		"latency_ms": latency,
	})
}

// GetHostStats 获取宿主机实时状态
func (h *HostHandler) GetHostStats(c *gin.Context) {
	id := c.Param("id")

	var host model.Host
	if err := h.DB.First(&host, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "宿主机不存在", err)
		return
	}

	// 获取系统信息
	hostname, _ := exec.Command("hostname").Output()
	kernel, _ := exec.Command("uname", "-r").Output()
	cpus, _ := exec.Command("nproc").Output()
	free, _ := exec.Command("free", "-h").Output()
	uptime, _ := exec.Command("uptime", "-p").Output()

	lines := strings.Split(string(free), "\n")
	var memTotal, memUsed string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Mem:") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				memTotal = fields[1]
				memUsed = fields[2]
			}
		}
	}

	Success(c, gin.H{
		"hostname":     strings.TrimSpace(string(hostname)),
		"kernel":       strings.TrimSpace(string(kernel)),
		"cpu_cores":    strings.TrimSpace(string(cpus)),
		"memory_total": memTotal,
		"memory_used":  memUsed,
		"uptime":       strings.TrimSpace(string(uptime)),
	})
}
