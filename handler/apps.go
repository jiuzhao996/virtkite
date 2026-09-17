package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/apps"
	"github.com/jiuzhao/vmops/service/tasks"
	"gorm.io/gorm"
)

// AppsHandler 应用商店处理器：内置应用目录查询与一键安装。
// 安装走 app_install 异步任务（在虚拟机内经 SSH 执行内置脚本，脚本正文见 service/apps）。
type AppsHandler struct {
	DB    *gorm.DB
	Tasks *tasks.Manager
}

// NewAppsHandler 创建应用商店处理器
func NewAppsHandler(db *gorm.DB, taskMgr *tasks.Manager) *AppsHandler {
	return &AppsHandler{DB: db, Tasks: taskMgr}
}

// List 应用目录。Detect/Install 无 json tag 不参与序列化，响应天然不含脚本正文。
func (h *AppsHandler) List(c *gin.Context) {
	Success(c, apps.All())
}

// Get 单应用详情：含检测/安装脚本预览（便于管理员安装前审查将在 VM 内执行的内容）。
func (h *AppsHandler) Get(c *gin.Context) {
	app, ok := apps.Get(c.Param("id"))
	if !ok {
		Fail(c, http.StatusNotFound, "应用不存在")
		return
	}
	Success(c, gin.H{
		"id":       app.ID,
		"name":     app.Name,
		"desc":     app.Desc,
		"category": app.Category,
		"detect":   app.Detect,
		"install":  app.Install,
	})
}

// Install 一键安装应用（异步）：校验 VM 存在性与授权后 Submit app_install 任务，
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
func (h *AppsHandler) Install(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusServiceUnavailable, "任务系统未初始化")
		return
	}
	var req struct {
		VMID     uint   `json:"vm_id"`
		AppID    string `json:"app_id"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	// vm_id 经 JSON 绑定为 uint 后再交 GORM（与 paramID「先解析后查询」同一约束，
	// uint 走参数化条件，无注入面）；vm_id 在请求体而非路径中，故不经过 paramID。
	if req.VMID == 0 {
		Fail(c, http.StatusBadRequest, "虚拟机 ID 不能为空")
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, req.VMID).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	// 授权决定可见性：非 admin 未持有效授权与不存在同响应
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	app, ok := apps.Get(req.AppID)
	if !ok {
		Fail(c, http.StatusBadRequest, "应用不存在")
		return
	}
	if req.Host == "" {
		Fail(c, http.StatusBadRequest, "目标主机地址不能为空")
		return
	}
	if req.User == "" {
		Fail(c, http.StatusBadRequest, "SSH 用户名不能为空")
		return
	}
	if req.Password == "" {
		Fail(c, http.StatusBadRequest, "SSH 密码不能为空")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.Port < 1 || req.Port > 65535 {
		Fail(c, http.StatusBadRequest, "SSH 端口必须在 1-65535 之间")
		return
	}

	payload := map[string]interface{}{
		"app_id":   app.ID,
		"host":     req.Host,
		"port":     req.Port,
		"user":     req.User,
		"password": req.Password,
	}
	userID, username := taskUserFromContext(c)
	vmID := vm.ID
	task, err := h.Tasks.Submit("app_install", "安装 "+app.Name+" 到 "+vm.Name,
		payload, userID, username, vm.Name, &vmID)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交安装任务失败", err)
		return
	}
	Accepted(c, "安装任务已提交", gin.H{"task_id": task.ID})
}
