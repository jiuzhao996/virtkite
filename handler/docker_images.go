// docker_images.go：容器/镜像管理——镜像列表/删除/拉取与清理。
// Prune 同时覆盖悬空镜像与已停止容器的清理；tailLines 供拉取输出取尾部。
package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
)

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
