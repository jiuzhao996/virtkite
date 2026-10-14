// docker_networks.go：容器/镜像管理——docker 网络（列表/详情/增删）+ 容器网络接入。
// dockerNetworkDrivers 为网络驱动白名单；Connect/Disconnect 完成容器与网络的双向挂接。
package handler

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ─────────────── docker 网络 ───────────────

// dockerNetworkDrivers 网络驱动白名单（与 dockerx.CreateNetwork 双层校验，此处给 400）。
var dockerNetworkDrivers = map[string]bool{
	"bridge": true, "overlay": true, "macvlan": true, "ipvlan": true, "host": true, "none": true,
}

// NetworkDetail GET /api/docker/networks/:name —— 单个 docker 网络详情（网段 + 挂接容器）。
func (h *DockerHandler) NetworkDetail(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	name := c.Param("name")
	detail, err := h.Docker.NetworkDetail(name)
	if err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "获取网络详情失败", err)
		return
	}
	Success(c, detail)
}

// ListNetworks GET /api/docker/networks
func (h *DockerHandler) ListNetworks(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	list, err := h.Docker.Networks()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	// 网段/网关批量补齐（一次 inspect 多网络）：卡片展示与 libvirt 虚拟网络对齐
	names := make([]string, 0, len(list))
	for _, n := range list {
		names = append(names, n.Name)
	}
	details, _ := h.Docker.NetworkDetails(names) // 失败按空详情降级，列表照常出
	items := make([]gin.H, 0, len(list))
	for _, n := range list {
		item := gin.H{
			"Name": n.Name, "Driver": n.Driver, "Scope": n.Scope,
			"IPv6": n.IPv6, "Internal": n.Internal, "CreatedAt": n.CreatedAt,
		}
		if d, ok := details[n.Name]; ok {
			item["Subnet"] = d.Subnet
			item["Gateway"] = d.Gateway
		}
		items = append(items, item)
	}
	Success(c, gin.H{"total": len(items), "items": items})
}

// CreateNetwork POST /api/docker/networks {"name","driver","subnet","gateway"}（subnet/gateway 可空）
func (h *DockerHandler) CreateNetwork(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Driver  string `json:"driver"`
		Subnet  string `json:"subnet"`
		Gateway string `json:"gateway"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !safeDockerID(body.Name) {
		Fail(c, http.StatusBadRequest, "网络名非法（仅允许字母数字与 -_.）")
		return
	}
	if body.Driver == "" {
		body.Driver = "bridge"
	}
	if !dockerNetworkDrivers[body.Driver] {
		Fail(c, http.StatusBadRequest, "网络驱动不支持（bridge/overlay/macvlan/ipvlan/host/none）")
		return
	}
	if body.Subnet != "" {
		if _, _, e := net.ParseCIDR(body.Subnet); e != nil {
			Fail(c, http.StatusBadRequest, "subnet 必须是合法 CIDR，如 172.30.0.0/16")
			return
		}
	}
	if body.Gateway != "" && net.ParseIP(body.Gateway) == nil {
		Fail(c, http.StatusBadRequest, "gateway 必须是合法 IP 地址")
		return
	}
	if err := h.Docker.CreateNetwork(body.Name, body.Driver, body.Subnet, body.Gateway); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"name": body.Name, "message": "网络已创建"})
}

// RemoveNetwork DELETE /api/docker/networks/:name
func (h *DockerHandler) RemoveNetwork(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	name := c.Param("name")
	if !safeDockerID(name) {
		Fail(c, http.StatusBadRequest, "网络名非法")
		return
	}
	if err := h.Docker.RemoveNetwork(name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"name": name, "message": "网络已删除"})
}

// ConnectContainerNetwork POST /api/docker/containers/:id/networks body {network}
// —— 把容器接入指定网络（docker network connect），补上「建得出网络、容器接不进」的闭环。
func (h *DockerHandler) ConnectContainerNetwork(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	if !safeDockerID(id) {
		Fail(c, http.StatusBadRequest, "容器 ID 非法")
		return
	}
	var body struct {
		Network string `json:"network"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Network) == "" {
		Fail(c, http.StatusBadRequest, "缺少 network 参数")
		return
	}
	if err := h.Docker.ConnectNetwork(body.Network, id); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "接入网络失败", err)
		return
	}
	Success(c, gin.H{"message": "已接入网络 " + body.Network})
}

// DisconnectContainerNetwork DELETE /api/docker/containers/:id/networks/:name
func (h *DockerHandler) DisconnectContainerNetwork(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	if !safeDockerID(id) {
		Fail(c, http.StatusBadRequest, "容器 ID 非法")
		return
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		Fail(c, http.StatusBadRequest, "缺少网络名")
		return
	}
	if err := h.Docker.DisconnectNetwork(name, id); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "断开网络失败", err)
		return
	}
	Success(c, gin.H{"message": "已断开网络 " + name})
}
