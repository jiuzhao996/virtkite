package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/tasks"
	"gorm.io/gorm"
)

// TaskHandler 异步任务查询处理器（对应契约 GET/DELETE /api/tasks）。
type TaskHandler struct {
	DB    *gorm.DB
	Tasks *tasks.Manager
}

// NewTaskHandler 创建任务查询处理器
func NewTaskHandler(db *gorm.DB, taskMgr *tasks.Manager) *TaskHandler {
	return &TaskHandler{DB: db, Tasks: taskMgr}
}

// ListTasks 任务列表 GET /api/tasks?status=&page=&page_size=（page 从 1 起，page_size 默认 50）。
// 返回 {total, items}，total 为筛选条件下的真实总数；兼容旧 limit 参数（等价 page_size）。
func (h *TaskHandler) ListTasks(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	status := c.Query("status")
	page, pageSize := parsePageQuery(c, 50, 200)
	items, total, err := h.Tasks.ListPaged(page, pageSize, status)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询任务列表失败", err)
		return
	}
	if items == nil {
		items = []model.Task{}
	}
	Success(c, gin.H{
		"total": total,
		"items": items,
	})
}

// GetTask 单个任务 GET /api/tasks/:id（含 result/error，不含 payload）。
func (h *TaskHandler) GetTask(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		Fail(c, http.StatusBadRequest, "任务 ID 不合法")
		return
	}
	task, err := h.Tasks.Get(uint(id))
	if err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "任务不存在", err)
		return
	}
	Success(c, task)
}

// CancelTask POST /api/tasks/:id/cancel —— 用户取消运行中的任务（AU2）。
// 仅 running 可取消；不响应 ctx 的 executor（libvirt 类）底层操作可能继续跑完，
// 但任务状态以取消为准。幂等：非 running 返回「不在运行中」不算错误。
func (h *TaskHandler) CancelTask(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	cancelled, err := h.Tasks.Cancel(id)
	if err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "任务不存在", err)
		return
	}
	if !cancelled {
		Fail(c, http.StatusConflict, "任务不在运行中，无法取消")
		return
	}
	Success(c, gin.H{"message": "已取消", "id": id})
}

// DeleteTask 删除任务 DELETE /api/tasks/:id，仅 finished（success/failed）可删。
func (h *TaskHandler) DeleteTask(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		Fail(c, http.StatusBadRequest, "任务 ID 不合法")
		return
	}
	task, err := h.Tasks.Get(uint(id))
	if err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "任务不存在", err)
		return
	}
	if task.Status != "success" && task.Status != "failed" {
		Fail(c, http.StatusConflict, "仅允许删除已完成(success/failed)的任务")
		return
	}
	if err := h.DB.Delete(&model.Task{}, task.ID).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "删除任务失败", err)
		return
	}
	Success(c, gin.H{"message": "任务已删除", "id": task.ID})
}
