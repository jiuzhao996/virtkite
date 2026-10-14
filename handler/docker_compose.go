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
func (h *DockerHandler) ComposeList(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	list, err := h.Docker.ComposeList()
	if err != nil {
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
func (h *DockerHandler) ComposeAction(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	name := c.Param("name")
	if !safeDockerID(name) {
		Fail(c, http.StatusBadRequest, "项目名非法")
		return
	}
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
func (h *DockerHandler) ComposeServices(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	name := c.Param("name")
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
	// 草稿栈：data/stacks/<id>/docker-compose.yml 存在但不在 compose ls 里
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
	sort.Slice(items, func(i, j int) bool {
		return items[i]["name"].(string) < items[j]["name"].(string)
	})
	Success(c, gin.H{"total": len(items), "items": items})
}

// draftStackIDs 扫描 data/stacks 下的栈目录（含 docker-compose.yml 者），返回栈 ID 列表。
// 目录不存在（尚未部署任何栈）时返回空，不报错。
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
		if safeDockerID(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}
