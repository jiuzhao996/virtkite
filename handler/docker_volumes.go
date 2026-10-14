// docker_volumes.go：容器/镜像管理——docker 卷（列表/创建/清理/删除）。
// PruneVolumes 清理未被容器占用的卷并回报释放空间。
package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
)

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
