package handler

import (
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/virt"
)

// NetworkMember 挂在该网络上的虚拟机网卡（详情抽屉成员表）。
type NetworkMember struct {
	VM    string `json:"vm"`
	MAC   string `json:"mac"`
	IP    string `json:"ip"`
	State string `json:"state"`
	Model string `json:"model"`
}

// validIPv4 校验是否为合法的点分十进制 IPv4 地址（网关将写入 libvirt <ip address>）。
// 额外要求规范写法（ip.String() 与输入一致），借此排除 IPv6 与 ::ffff:1.2.3.4 之类映射写法；
// 与 virt 层的 encoding/xml 序列化构成纵深防御。
func validIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil && ip.String() == s
}

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

// GetNetwork 网络详情（含 XML + 成员表）。成员=挂在该网络上的虚拟机网卡，IP 尽力由 DHCP 租约补齐。
func (h *NetworkHandler) GetNetwork(c *gin.Context) {
	name := c.Param("name")
	info, err := h.Virt.GetNetwork(name)
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, err)
		return
	}

	// subnet：libvirt 的 cidr 字段实为 netmask，这里换算成真正网段便于展示（与拓扑端点同源函数）
	Success(c, gin.H{"network": info, "members": h.networkMembers(info), "subnet": netCIDR(info.Gateway, info.CIDR)})
}

// networkMembers 枚举挂在该网络上的虚拟机网卡（含关机域），并按 MAC 补 DHCP 租约 IP。
// 匹配条件：网卡 source == 网络名；或 source == 该网络网桥（bridge 直连域）。
func (h *NetworkHandler) networkMembers(info *virt.NetworkInfo) []NetworkMember {
	members := []NetworkMember{}
	doms, err := h.Virt.ListAllDomainNetworks()
	if err != nil {
		return members
	}
	leaseByMAC := map[string]string{}
	if leases, lerr := h.Virt.ListDHCPLeases(); lerr == nil {
		for _, l := range leases {
			if l.MAC != "" {
				leaseByMAC[strings.ToLower(l.MAC)] = l.IP
			}
		}
	}
	for _, d := range doms {
		for _, ifc := range d.Interfaces {
			if ifc.Source != info.Name && (info.Bridge == "" || ifc.Source != info.Bridge) {
				continue
			}
			members = append(members, NetworkMember{
				VM: d.Name, MAC: ifc.MAC, IP: leaseByMAC[strings.ToLower(ifc.MAC)],
				State: d.State, Model: ifc.Model,
			})
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].VM < members[j].VM })
	return members
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
	// gateway 留空时 virt 层用默认网关（192.168.100.1），保持原行为；非空则必须是合法 IPv4
	if req.Gateway != "" && !validIPv4(req.Gateway) {
		Fail(c, http.StatusBadRequest, "网关必须是合法的 IPv4 地址")
		return
	}

	xml := virt.NetworkXMLFromParams(req.Name, "", req.Gateway)
	if err := h.Virt.DefineNetwork(xml); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": req.Name, "gateway": req.Gateway})
}

// SetNetworkAutostart 设置网络自启动 PUT /api/networks/:name/autostart（body: {autostart: bool}）。
func (h *NetworkHandler) SetNetworkAutostart(c *gin.Context) {
	name := c.Param("name")
	var req struct {
		Autostart *bool `json:"autostart" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误：autostart 必须为布尔值")
		return
	}
	if err := h.Virt.SetNetworkAutostart(name, *req.Autostart); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"name": name, "autostart": *req.Autostart})
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
