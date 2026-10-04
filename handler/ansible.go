// ansible.go：运维自动化（P4 S1）——引擎状态探测 + adhoc 批量执行入口。
// 目标只收 vm_id（服务端查库取 IP/凭据，白名单结构性成立）；执行走 ansible_run
// 异步任务，输出随任务 Result 实时刷新（前端轮询任务详情）。
package handler

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/ansible"
	"github.com/jiuzhao/vmops/service/tasks"
	"gorm.io/gorm"
)

// AnsibleHandler 运维自动化处理器。
type AnsibleHandler struct {
	DB    *gorm.DB
	Tasks *tasks.Manager
}

// NewAnsibleHandler 创建处理器。
func NewAnsibleHandler(db *gorm.DB, taskMgr *tasks.Manager) *AnsibleHandler {
	return &AnsibleHandler{DB: db, Tasks: taskMgr}
}

// S1 允许的 adhoc 模块：ping=连通性、command=无 shell 命令、shell=完整 shell。
// 本来就是运维场景（root 批量执行），allowlist 收敛的是「误用面」而非权限面。
var ansibleModules = map[string]bool{"ping": true, "command": true, "shell": true}

// Status GET /api/ansible/status —— 引擎探测（路径 + 版本），未装返回引导信息。
func (h *AnsibleHandler) Status(c *gin.Context) {
	eng, err := ansible.Detect()
	if err != nil {
		Success(c, gin.H{
			"installed": false,
			"hint":      "宿主机未安装 ansible（pip install --user ansible 或系统包管理器安装后刷新）",
		})
		return
	}
	Success(c, gin.H{
		"installed": true,
		"path":      eng.PlaybookPath,
		"version":   eng.Version,
	})
}

// Run POST /api/ansible/run —— adhoc 批量执行（operator+，路由组已挡）。
// body: {targets*[vm_id], module*(ping|command|shell), args?}
func (h *AnsibleHandler) Run(c *gin.Context) {
	var req struct {
		Targets []uint `json:"targets"`
		Module  string `json:"module"`
		Args    string `json:"args"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数格式非法")
		return
	}
	if len(req.Targets) == 0 {
		Fail(c, http.StatusBadRequest, "请选择目标虚拟机")
		return
	}
	if len(req.Targets) > 50 {
		Fail(c, http.StatusBadRequest, "单次执行最多 50 台")
		return
	}
	req.Module = strings.TrimSpace(req.Module)
	if !ansibleModules[req.Module] {
		Fail(c, http.StatusBadRequest, "模块仅支持 ping / command / shell")
		return
	}
	if req.Module != "ping" && strings.TrimSpace(req.Args) == "" {
		Fail(c, http.StatusBadRequest, "模块 "+req.Module+" 需要填写执行参数")
		return
	}
	// 轻校验目标存在性（权威校验在 executor：运行态/IP/凭据逐台复核）
	var cnt int64
	if err := h.DB.Model(&model.VM{}).Where("id IN ?", req.Targets).Count(&cnt).Error; err != nil || cnt != int64(len(req.Targets)) {
		Fail(c, http.StatusBadRequest, "部分目标虚拟机不存在")
		return
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("ansible_run",
		"Ansible "+req.Module+" → "+strconv.Itoa(len(req.Targets))+" 台",
		map[string]interface{}{"targets": req.Targets, "module": req.Module, "args": req.Args},
		userID, username, "", nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	// 留痕：谁、从哪、对几台机器跑了什么模块（参数不进日志——shell 参数可能含敏感内容）
	log.Printf("[ansible] 提交批量执行 module=%s targets=%d user=%s from=%s",
		req.Module, len(req.Targets), username, c.ClientIP())
	Accepted(c, "批量执行任务已提交", gin.H{"task_id": task.ID})
}
