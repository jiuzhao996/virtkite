package handler

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
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
		h.DB.Model(&host).Update("status", "unreachable")
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
	// 连通则回写状态（列表不再 unknown）
	h.DB.Model(&host).Update("status", "reachable")

	Success(c, gin.H{
		"reachable":  true,
		"latency_ms": latency,
	})
}

// GetHostStats 获取宿主机实时状态（/proc 直读，与 dashboard 同源，不依赖 free/uptime 文本解析）。
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

	// 内存：读 /proc/meminfo（KB），展示为人类可读
	memTotalKB, memAvailKB := readHostMeminfo()
	memTotal, memUsed := "", ""
	if memTotalKB > 0 {
		memTotal = formatBytes(memTotalKB * 1024)
		if memTotalKB > memAvailKB {
			memUsed = formatBytes((memTotalKB - memAvailKB) * 1024)
		}
	}

	Success(c, gin.H{
		"hostname":     strings.TrimSpace(string(hostname)),
		"kernel":       strings.TrimSpace(string(kernel)),
		"cpu_cores":    strings.TrimSpace(string(cpus)),
		"memory_total": memTotal,
		"memory_used":  memUsed,
		"uptime":       formatUptimeCN(readHostUptimeSec()),
	})
}

// readHostMeminfo 读 MemTotal/MemAvailable（单位 KB）。
func readHostMeminfo() (total, avail uint64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, _ = strconv.ParseUint(f[1], 10, 64)
		case "MemAvailable:":
			avail, _ = strconv.ParseUint(f[1], 10, 64)
		}
	}
	return total, avail
}

// formatBytes 字节转人类可读（B/KB/MB/GB）。
func formatBytes(b uint64) string {
	const unit = 1024.0
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	f := float64(b)
	for _, u := range []string{"KB", "MB", "GB", "TB"} {
		f /= unit
		if f < 1024 || u == "TB" {
			return fmt.Sprintf("%.1f %s", f, u)
		}
	}
	return fmt.Sprintf("%d B", b)
}

// readHostUptimeSec 读 /proc/uptime 首字段（秒）。
func readHostUptimeSec() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return int64(v)
}

// formatUptimeCN 秒转中文运行时长（X 天 X 小时 X 分钟）。
func formatUptimeCN(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%d 天 %d 小时 %d 分钟", d, h, m)
	}
	if h > 0 {
		return fmt.Sprintf("%d 小时 %d 分钟", h, m)
	}
	return fmt.Sprintf("%d 分钟", m)
}
