// docker_containers.go：容器/镜像管理——容器生命周期与只读文件浏览。
// 列表/创建/启停重启暂停/删除/日志/详情/实时统计/资源更新/容器内文件查看。
//
// 统一约定：
//   - 每个端点先调 h.dockerAvailable：Docker 未安装/未运行/无权限时回统一中文
//     错误并中止，后续无需重复判空；
//   - 容器 ID 来自 URL，必须过 safeDockerID（长度/字符白名单）再交给 dockerx，
//     避免把任意字符串拼进 docker CLI 参数；
//   - handler 只做参数校验与错误边界，实际 docker 调用全在 service/dockerx；
//     内部错误经 ErrorResponse/ErrorWithMessage 记录，前端只拿中文提示。

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
// 返回全部容器（含已停止），排序/字段由 dockerx.Containers 决定；前端列表页
// 直接消费，total 与 items 同源，避免前端再算一次长度。
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

// ContainerAction POST /api/docker/containers/:id/:action（start/stop/restart/pause/unpause/rename）
//
// 用 URL 里的 :action 分发而不是为每个动作建路由：动作集合固定、语义同属
// 「对同一容器的一次操作」，合并后前端只需一个调用入口。未知动作回 404；
// rename 额外要求 JSON body {"name": "..."}，其余动作忽略请求体。
// 错误统一 500（docker 侧失败），成功文案由 label 拼出「已启动/已停止…」。
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
// force=true 对应 docker rm -f（运行中也强删）；默认只删已停止容器，运行中
// 由 docker 侧报错，避免误删业务容器。
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
// tail 解析失败时 Atoi 返回 0，dockerx 会把 <=0 或 >2000 的值回退成 200；
// timestamps=1 时附带时间戳。日志本身可能很大，前端用 tail 控制返回量。
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
// 返回 docker inspect 的对象本体（不做字段裁剪），供详情抽屉展示原始配置。
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
//
// 只允许运行时可改的项（CPU/内存/健康检查等），ValidateRuntimeLimits 负责格式
// 校验，HasRuntimeUpdate 拒绝「空更新」（否则会白跑一次 docker update）。
// 这两类都是参数问题，回 400 并带固定中文原因；docker 执行失败才是 500。
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
//
// path 缺省为容器根 /；SafeContainerPath 要求容器内绝对路径，并拒绝空字节/
// 换行等会污染 exec 参数的字符（`..` 无需特别处理：路径只由容器内进程解析，
// 不涉及宿主机文件系统）。容器必须处于运行中（docker exec 的前提），未运行时
// dockerx 返回中文友好错误。
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
// 读取有字节上限（dockerx.maxContainerFileBytes），超出时 truncated=true 并返回
// 截断内容；疑似二进制（含空字节）直接拒绝，避免把乱码塞给前端编辑器。
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
