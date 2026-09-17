package handler

import (
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
