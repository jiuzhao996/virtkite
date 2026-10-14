// docker_containers.go：容器/镜像管理——容器生命周期与只读文件浏览。
// 列表/创建/启停重启暂停/删除/日志/详情/实时统计/资源更新/容器内文件查看。
package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
)

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

// CreateContainer POST /api/docker/containers（v3.2 规划 R8 收尾：一键创建并启动容器）。
// 请求体为 dockerx.ContainerOpts（name/image 必填，其余可空）；校验失败 400 固定中文文案，
// docker run 失败 500（完整错误进日志）。成功返回 {"id": "<容器短ID>"}。
// 仅 admin（二次收口）：容器创建可挂载宿主机任意路径（-v /:/host 即整机文件系统可读改），
// 等于把宿主机 shell 交给请求方。docker 路由组挂 NonViewerMiddleware 只挡 viewer，
// operator 的写请求能直达本端点，故 handler 内收口仅 admin
// （与 UpdateVMXML 的内联 roleIsAdmin 闸同款角色闸，单独给文案）。
func (h *DockerHandler) CreateContainer(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "容器创建仅管理员可用")
		return
	}
	if !h.dockerAvailable(c) {
		return
	}
	var body dockerx.ContainerOpts
	if err := c.ShouldBindJSON(&body); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Image = strings.TrimSpace(body.Image)
	// 校验错误是固定中文文案（无内部细节），可直接回显——与 UpdateSettings 的 setting.Validate 同口径
	if err := dockerx.ValidateContainerOpts(body); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.Docker.CreateContainer(body)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"id": id})
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
	case "pause":
		err, label = h.Docker.Pause(id), "暂停"
	case "unpause":
		err, label = h.Docker.Unpause(id), "恢复"
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
	logs, err := h.Docker.Logs(id, tail, c.Query("timestamps") == "1")
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"id": id, "logs": logs})
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
// 结果缓存 statsAllCacheTTL（3s）：docker stats --no-stream 每次都要先吃一个
// ~2s 的 CPU 采样窗才出数，轮询场景每次现跑既慢又徒增 docker 负担；
// 3s 对「实时资源列」的体感无差。失败不缓存，下个请求立即重试。
func (h *DockerHandler) StatsAll(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	h.statsMu.Lock()
	defer h.statsMu.Unlock()
	if h.statsCached != nil && time.Since(h.statsAt) < statsAllCacheTTL {
		list := h.statsCached
		Success(c, gin.H{"total": len(list), "items": list})
		return
	}
	list, err := h.Docker.StatsAll()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	h.statsCached, h.statsAt = list, time.Now()
	Success(c, gin.H{"total": len(list), "items": list})
}

// UpdateContainer POST /api/docker/containers/:id/update —— 动态调整资源限制与健康检查（docker update）。
func (h *DockerHandler) UpdateContainer(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	if !safeDockerID(id) {
		Fail(c, http.StatusBadRequest, "容器 ID 非法")
		return
	}
	var opts dockerx.ContainerOpts
	if err := c.ShouldBindJSON(&opts); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	// 参数类错误（格式非法 / 无可更新项）回 400 并带原因；docker 侧失败才是 500
	if err := dockerx.ValidateRuntimeLimits(opts); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if !dockerx.HasRuntimeUpdate(opts) {
		Fail(c, http.StatusBadRequest, "没有可更新的项（资源限制或健康检查参数至少填一个）")
		return
	}
	if err := h.Docker.UpdateContainer(id, opts); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "更新容器失败", err)
		return
	}
	Success(c, gin.H{"message": "已更新"})
}

// ─────────────── 容器内文件浏览（只读，借 1Panel file-browser） ───────────────

// ContainerFiles GET /api/docker/containers/:id/files?path=/ —— 列出容器内目录。
// 只读能力：仅 ls/cat，不含上传/删除（那是另一个量级的风险面）。
func (h *DockerHandler) ContainerFiles(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	if !safeDockerID(id) {
		Fail(c, http.StatusBadRequest, "容器 ID 非法")
		return
	}
	path := c.Query("path")
	if path == "" {
		path = "/"
	}
	if !dockerx.SafeContainerPath(path) {
		Fail(c, http.StatusBadRequest, "路径非法（须为容器内绝对路径）")
		return
	}
	// dockerx 侧的「容器未运行 / 缺 ls」已转成中文友好文案，ErrorResponse 会原样透出
	entries, err := h.Docker.ListContainerDir(id, path)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"path": path, "total": len(entries), "items": entries})
}

// ContainerFileContent GET /api/docker/containers/:id/file?path=/etc/hosts —— 读取容器内文本文件。
func (h *DockerHandler) ContainerFileContent(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	id := c.Param("id")
	if !safeDockerID(id) {
		Fail(c, http.StatusBadRequest, "容器 ID 非法")
		return
	}
	path := c.Query("path")
	if !dockerx.SafeContainerPath(path) {
		Fail(c, http.StatusBadRequest, "路径非法（须为容器内绝对路径）")
		return
	}
	content, truncated, err := h.Docker.ReadContainerFile(id, path)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"path": path, "content": content, "truncated": truncated, "size": len(content)})
}
