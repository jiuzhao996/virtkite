package handler

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/service/dockerx"
)

// DockerHandler 的 compose 编排端点（v3.2 批次 R）。单独成文件：compose 是
// 「项目级」编排语义（一个 project 对应一组容器），与单容器 CRUD 分开维护。

// ComposeList GET /api/docker/compose
// 列出已部署的 compose 项目（等价 `docker compose ls`）。docker 不可用时
// dockerAvailable 已写好 503 响应，调用方直接 return 即可。
func (h *DockerHandler) ComposeList(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	list, err := h.Docker.ComposeList()
	if err != nil {
		// docker CLI 的 stderr 属于内部细节：完整错误进日志，响应只给中文文案
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"total": len(list), "items": list})
}

// composeWriteErr 写类 compose 操作的错误映射。
//
// 栈级锁冲突不是「服务端故障」而是「稍后再试」——统一走 500 会让前端只能提示
// 「操作失败」，用户不知道重试一下就好。这里把它单列成 409，语义与 VM 的 lockVM 冲突一致。
func composeWriteErr(c *gin.Context, err error) {
	if errors.Is(err, dockerx.ErrStackBusy) {
		Fail(c, http.StatusConflict, "该栈正在执行其他操作，请稍后再试")
		return
	}
	ErrorResponse(c, http.StatusInternalServerError, err)
}

// ComposeAction POST /api/docker/compose/:name/:action
// action 白名单 up/stop/down/start/restart，由 dockerx.ComposeAction 兜底二次校验。
// 这里是「项目级」生命周期操作；服务级重启见 ComposeServiceAction。
func (h *DockerHandler) ComposeAction(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	name := c.Param("name")
	// 项目名会作为 `docker compose -p <name>` 参数，并拼进 data/stacks/<name> 路径，
	// 必须先过白名单，避免命令注入与目录穿越（safeDockerID 只放行安全字符集）。
	if !safeDockerID(name) {
		Fail(c, http.StatusBadRequest, "项目名非法")
		return
	}
	// 成功文案在服务端统一给出：前端无需为每个 action 各维护一份映射，
	// 也保证 API 调用方拿到的提示与 Web UI 一致。
	var message string
	action := c.Param("action")
	switch action {
	case "up":
		message = "项目已后台启动"
	case "start":
		message = "项目已启动"
	case "stop":
		message = "项目已停止"
	case "restart":
		message = "项目已重启"
	case "down":
		// down 会移除容器与网络但保留具名卷，文案把这一取舍明确告诉用户
		message = "项目已下线（容器与网络已移除，具名卷保留）"
	default:
		Fail(c, http.StatusNotFound, "未知操作")
		return
	}
	if err := h.Docker.ComposeAction(name, action); err != nil {
		composeWriteErr(c, err)
		return
	}
	Success(c, gin.H{"name": name, "action": action, "message": message})
}

// ComposeServices GET /api/docker/compose/:name/services —— 项目内服务容器列表。
// 返回的是「服务 → 容器」展开结果，供栈详情页展示每个 service 的副本与状态。
func (h *DockerHandler) ComposeServices(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	name := c.Param("name")
	// 与 ComposeAction 同口径：项目名参与 docker CLI 参数，必须先白名单校验
	if !safeDockerID(name) {
		Fail(c, http.StatusBadRequest, "项目名非法")
		return
	}
	list, err := h.Docker.ComposeServices(name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"total": len(list), "items": list})
}

// ComposeServiceAction POST /api/docker/compose/:name/services/:service/:action
// 服务级操作（当前仅 restart），白名单由 dockerx.ComposeServiceAction 二次校验。
// 项目名与服务名都来自 URL 且会进 docker CLI，两个都要过 safeDockerID。
func (h *DockerHandler) ComposeServiceAction(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	name, service := c.Param("name"), c.Param("service")
	if !safeDockerID(name) || !safeDockerID(service) {
		Fail(c, http.StatusBadRequest, "项目名或服务名非法")
		return
	}
	action := c.Param("action")
	if err := h.Docker.ComposeServiceAction(name, service, action); err != nil {
		composeWriteErr(c, err)
		return
	}
	Success(c, gin.H{"name": name, "service": service, "action": action, "message": "服务已重启"})
}

// ─────────────── compose 内容校验（借 Dockge 的即时校验，服务端权威解析） ───────────────

// ValidateCompose POST /api/docker/compose/validate body {"content":"..."}
// 用 docker compose -f <临时文件> config 做真解析，返回结构化错误（行号 + 消息）。
// 收口 admin：compose config 会解析 include/env_file 等本地引用，属文件读取面，
// 与 stacks.File 同口径仅管理员可用（编辑 compose 本就是管理员能力）。
// 解析由 dockerx 落临时文件后调用 CLI，本 handler 只负责鉴权、限长与错误出口。
func (h *DockerHandler) ValidateCompose(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "compose 校验仅管理员可用")
		return
	}
	if !h.dockerAvailable(c) {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	// 1MB 上限：compose 文件正常只有几 KB，限制体积避免超大请求拖垮 CLI 与内存
	if len(body.Content) > 1<<20 {
		Fail(c, http.StatusBadRequest, "compose 内容过大（>1MB）")
		return
	}
	res, err := h.Docker.ComposeValidate(body.Content)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, res)
}

// ─────────────── compose 栈状态机（借 Dockge 的 5 态模型） ───────────────

// ComposeStates GET /api/docker/compose/states —— 每个 compose 栈的状态。
// 在 compose ls（已部署）基础上补上 data/stacks 下「文件在但未部署」的草稿栈，
// 交给 dockerx.ComposeStackState 统一判定（任一服务 exited 即整栈 EXITED）。
// 状态机放在 dockerx 而不是 handler：同一套判定也被前端轮询/其它入口复用。
func (h *DockerHandler) ComposeStates(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	projects, err := h.Docker.ComposeList()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	items := []gin.H{}
	seen := map[string]bool{}
	for _, p := range projects {
		// 单个项目的服务查询失败不拖垮整个列表：按空服务降级，
		// 状态机仍能给出「已部署但服务未知」的保守结果。
		services, _ := h.Docker.ComposeServices(p.Name) // 失败按空服务降级
		items = append(items, gin.H{
			"name":         p.Name,
			"state":        dockerx.ComposeStackState(true, true, services),
			"deployed":     true,
			"has_file":     true,
			"status":       p.Status,
			"config_files": p.ConfigFiles,
		})
		seen[p.Name] = true
	}
	// 草稿栈：data/stacks/<id>/docker-compose.yml 存在但不在 compose ls 里。
	// 这类栈尚未 `up`，没有容器可采样，因此 has_file=true / deployed=false。
	for _, name := range draftStackIDs() {
		if seen[name] {
			continue
		}
		items = append(items, gin.H{
			"name":     name,
			"state":    dockerx.ComposeStackState(true, false, nil),
			"deployed": false,
			"has_file": true,
		})
	}
	// 按名称排序，保证前端列表顺序稳定（compose ls 与目录遍历顺序都不保证）
	sort.Slice(items, func(i, j int) bool {
		return items[i]["name"].(string) < items[j]["name"].(string)
	})
	Success(c, gin.H{"total": len(items), "items": items})
}

// draftStackIDs 扫描 data/stacks 下的栈目录（含 docker-compose.yml 者），返回栈 ID 列表。
// 目录不存在（尚未部署任何栈）时返回空，不报错。
// 只认 docker-compose.yml 是刻意的：栈落盘入口（stacks.File）就写这个文件名，
// 不把 compose.yaml 等别名算进来，避免与「已部署」列表口径不一致。
func draftStackIDs() []string {
	entries, err := os.ReadDir(config.DataPath("stacks"))
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(config.DataPath("stacks", e.Name()), "docker-compose.yml")); err != nil {
			continue
		}
		// 目录名随后会进 docker CLI 与文件路径，同样过白名单，跳过非法目录
		if safeDockerID(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}
