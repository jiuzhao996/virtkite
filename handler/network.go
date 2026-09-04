package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/virt"
)

// NetworkHandler 网络处理器
type NetworkHandler struct {
	Virt *virt.Virt
}

// NewNetworkHandler 创建网络处理器
func NewNetworkHandler() *NetworkHandler {
	return &NetworkHandler{Virt: virt.New()}
}

// ListNetworks 网络列表
func (h *NetworkHandler) ListNetworks(c *gin.Context) {
	networks, err := h.Virt.ListNetworks()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取网络列表失败", err)
		return
	}

	Success(c, gin.H{
		"total": len(networks),
		"items": networks,
	})
}

// GetNetwork 网络详情（含 XML）
func (h *NetworkHandler) GetNetwork(c *gin.Context) {
	name := c.Param("name")
	info, err := h.Virt.GetNetwork(name)
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, err)
		return
	}

	Success(c, info)
}

// CreateNetwork 创建 NAT 网络
func (h *NetworkHandler) CreateNetwork(c *gin.Context) {
	var req struct {
		Name    string `json:"name" binding:"required"`
		Gateway string `json:"gateway"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "网络名称只允许字母、数字、下划线和连字符")
		return
	}

	xml := virt.NetworkXMLFromParams(req.Name, "", req.Gateway)
	if err := h.Virt.DefineNetwork(xml); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": req.Name, "gateway": req.Gateway})
}

// DefineNetworkXML 从 XML 定义网络
func (h *NetworkHandler) DefineNetworkXML(c *gin.Context) {
	var req struct {
		XML string `json:"xml" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	if err := h.Virt.DefineNetwork(req.XML); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"message": "网络已定义"})
}

// UpdateNetwork 编辑网络（body: {xml}，对应 virsh net-destroy + net-undefine + net-define + net-start）。
func (h *NetworkHandler) UpdateNetwork(c *gin.Context) {
	name := c.Param("name")
	var req struct {
		XML string `json:"xml" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	if err := h.Virt.UpdateNetwork(name, req.XML); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": name, "message": "网络已更新"})
}

// StartNetwork 启动网络
func (h *NetworkHandler) StartNetwork(c *gin.Context) {
	name := c.Param("name")
	if err := h.Virt.StartNetwork(name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": name})
}

// StopNetwork 停止网络
func (h *NetworkHandler) StopNetwork(c *gin.Context) {
	name := c.Param("name")
	if err := h.Virt.StopNetwork(name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": name})
}

// DeleteNetwork 删除网络
func (h *NetworkHandler) DeleteNetwork(c *gin.Context) {
	name := c.Param("name")
	if err := h.Virt.DeleteNetwork(name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": name})
}
