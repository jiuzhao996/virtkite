package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
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
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"name": name, "action": action, "message": message})
}
