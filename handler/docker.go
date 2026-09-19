package handler

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
)

// DockerHandler 容器/镜像管理（v2 大升级批次 1，对标宝塔的「服务管理」心智）。
type DockerHandler struct {
	Docker *dockerx.Dockerx
}

// NewDockerHandler 创建 DockerHandler。
func NewDockerHandler() *DockerHandler {
	return &DockerHandler{Docker: dockerx.New()}
}

// dockerAvailable 探测 docker CLI 可用性，不可用直接 503 + 引导文案。
func (h *DockerHandler) dockerAvailable(c *gin.Context) bool {
	ok, msg := h.Docker.Available()
	if !ok {
		ErrorWithMessage(c, http.StatusServiceUnavailable, "docker 不可用："+msg, nil)
		return false
	}
	return true
}

// ListContainers GET /api/docker/containers
func (h *DockerHandler) ListContainers(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	list, err := h.Docker.Containers()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"total": len(list), "items": list})
}

// ContainerAction POST /api/docker/containers/:id/:action（start/stop/restart）
func (h *DockerHandler) ContainerAction(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	if !safeDockerID(id) {
		Fail(c, http.StatusBadRequest, "容器 ID 非法")
		return
	}
	var err error
	var label string
	switch c.Param("action") {
	case "start":
		err, label = h.Docker.Start(id), "启动"
	case "stop":
		err, label = h.Docker.Stop(id), "停止"
	case "restart":
		err, label = h.Docker.Restart(id), "重启"
	case "rename":
		// v3.2 批次 R：重命名复用既有 :action 路由，请求体 {"name":"新名称"}
		var body struct {
			Name string `json:"name"`
		}
		if e := c.ShouldBindJSON(&body); e != nil || body.Name == "" {
			Fail(c, http.StatusBadRequest, `请在请求体中提供新名称 {"name": "..."}`)
			return
		}
		err, label = h.Docker.RenameContainer(id, body.Name), "重命名为 "+body.Name
	default:
		Fail(c, http.StatusNotFound, "未知操作")
		return
	}
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"id": id, "message": "已" + label})
}

// RemoveContainer DELETE /api/docker/containers/:id?force=true
func (h *DockerHandler) RemoveContainer(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	if !safeDockerID(id) {
		Fail(c, http.StatusBadRequest, "容器 ID 非法")
		return
	}
	if err := h.Docker.Remove(id, c.Query("force") == "true"); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"id": id, "message": "已删除"})
}

// ContainerLogs GET /api/docker/containers/:id/logs?tail=200
func (h *DockerHandler) ContainerLogs(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	if !safeDockerID(id) {
		Fail(c, http.StatusBadRequest, "容器 ID 非法")
		return
	}
	tail, _ := strconv.Atoi(c.DefaultQuery("tail", "200"))
	logs, err := h.Docker.Logs(id, tail)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"id": id, "logs": logs})
}

// ListImages GET /api/docker/images
func (h *DockerHandler) ListImages(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	list, err := h.Docker.Images()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"total": len(list), "items": list})
}

// RemoveImage DELETE /api/docker/images/:id
func (h *DockerHandler) RemoveImage(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	// 镜像 ID 允许 repo:tag 形式，比容器 ID 放宽一层，但仍禁空白与危险字符
	if id == "" || strings.ContainsAny(id, " \t;&|$`") {
		Fail(c, http.StatusBadRequest, "镜像 ID 非法")
		return
	}
	if err := h.Docker.RemoveImage(id); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"id": id, "message": "已删除"})
}

// ─────────────── 容器详情 / 实时统计 / 镜像拉取与清理（v3.2 批次 R） ───────────────

// InspectContainer GET /api/docker/containers/:id/inspect
func (h *DockerHandler) InspectContainer(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	if !safeDockerID(id) {
		Fail(c, http.StatusBadRequest, "容器 ID 非法")
		return
	}
	info, err := h.Docker.Inspect(id)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, info)
}

// ContainerStats GET /api/docker/containers/:id/stats（单容器实时资源占用）
func (h *DockerHandler) ContainerStats(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	if !safeDockerID(id) {
		Fail(c, http.StatusBadRequest, "容器 ID 非法")
		return
	}
	stats, err := h.Docker.Stats(id)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, stats)
}

// StatsAll GET /api/docker/stats（全容器实时资源占用，列表页资源列轮询用）
func (h *DockerHandler) StatsAll(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	list, err := h.Docker.StatsAll()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"total": len(list), "items": list})
}

// PullImage POST /api/docker/images/pull {"name":"nginx:latest"}
// 同步执行（10 分钟超时，前端 loading 等待），成功返回输出尾部（分层下载进度很长，只回结尾）。
func (h *DockerHandler) PullImage(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		Fail(c, http.StatusBadRequest, "请提供镜像名（如 nginx:latest）")
		return
	}
	out, err := h.Docker.Pull(name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"name": name, "message": "镜像拉取完成", "output": tailLines(out, 20)})
}

// Prune POST /api/docker/prune {"type":"images"|"containers"}，解析输出中的空间回收信息返回。
func (h *DockerHandler) Prune(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	var body struct {
		Type string `json:"type"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	var (
		out   string
		label string
		err   error
	)
	switch body.Type {
	case "images":
		out, err = h.Docker.PruneImages()
		label = "悬空镜像"
	case "containers":
		out, err = h.Docker.PruneContainers()
		label = "已停止容器"
	default:
		Fail(c, http.StatusBadRequest, "type 仅支持 images 或 containers")
		return
	}
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	freed := dockerx.ReclaimedSpace(out)
	message := "清理完成"
	if freed != "" {
		message = "清理" + label + "完成，释放空间 " + freed
	}
	Success(c, gin.H{"type": body.Type, "message": message, "freed": freed, "output": strings.TrimSpace(out)})
}

// tailLines 取文本尾部 n 行（pull 进度输出可达数千行，只回结尾给前端展示）。
func tailLines(s string, n int) string {
	s = strings.TrimRight(s, "\n\r")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ─────────────── docker 网络 ───────────────

// dockerNetworkDrivers 网络驱动白名单（与 dockerx.CreateNetwork 双层校验，此处给 400）。
var dockerNetworkDrivers = map[string]bool{
	"bridge": true, "overlay": true, "macvlan": true, "ipvlan": true, "host": true, "none": true,
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
	Success(c, gin.H{"total": len(list), "items": list})
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

// ─────────────── docker 卷 ───────────────

// ListVolumes GET /api/docker/volumes
func (h *DockerHandler) ListVolumes(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	list, err := h.Docker.Volumes()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"total": len(list), "items": list})
}

// CreateVolume POST /api/docker/volumes {"name":"..."}
func (h *DockerHandler) CreateVolume(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !safeDockerID(body.Name) {
		Fail(c, http.StatusBadRequest, "卷名非法（仅允许字母数字与 -_.）")
		return
	}
	if err := h.Docker.CreateVolume(body.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"name": body.Name, "message": "卷已创建"})
}

// PruneVolumes POST /api/docker/volumes/prune（清理未被容器占用的卷），返回释放的空间信息。
func (h *DockerHandler) PruneVolumes(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	out, err := h.Docker.PruneVolumes()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	freed := dockerx.ReclaimedSpace(out)
	message := "清理完成"
	if freed != "" {
		message = "清理卷完成，释放空间 " + freed
	}
	Success(c, gin.H{"message": message, "freed": freed, "output": strings.TrimSpace(out)})
}

// RemoveVolume DELETE /api/docker/volumes/:name
func (h *DockerHandler) RemoveVolume(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	name := c.Param("name")
	if !safeDockerID(name) {
		Fail(c, http.StatusBadRequest, "卷名非法")
		return
	}
	if err := h.Docker.RemoveVolume(name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"name": name, "message": "卷已删除"})
}

// safeDockerID 容器 ID/名称白名单：字母数字、下划线、连字符、点。
func safeDockerID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}
