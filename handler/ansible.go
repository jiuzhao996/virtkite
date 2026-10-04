// ansible.go：运维自动化（P4 S1）——引擎状态探测 + adhoc 批量执行入口。
// 目标只收 vm_id（服务端查库取 IP/凭据，白名单结构性成立）；执行走 ansible_run
// 异步任务，输出随任务 Result 实时刷新（前端轮询任务详情）。
package handler

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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

// playbook 目录约定：仓库内置种子（git 跟踪，ansible/playbooks/）首次启动复制到
// data/ansible/playbooks/（用户编辑区，重启不覆盖）；id = 文件名去 .yml。
var (
	playbookDir  = filepath.Join("data", "ansible", "playbooks")
	seedDir      = filepath.Join("ansible", "playbooks")
	playbookIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
)

// EnsureSeedPlaybooks 种子落盘：目录里缺哪个补哪个（用户删掉的会复活——种子语义
// 就是「出厂预设」；用户改过的同名文件不覆盖）。routes.go 装配时调用一次。
func EnsureSeedPlaybooks() {
	if err := os.MkdirAll(playbookDir, 0o755); err != nil {
		log.Printf("[ansible] 创建 playbook 目录失败: %v", err)
		return
	}
	entries, err := os.ReadDir(seedDir)
	if err != nil {
		return // 仓库内无种子目录（非源码运行），静默跳过
	}
	copied := 0
	for _, e := range entries {
		if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".yml" {
			continue
		}
		dst := filepath.Join(playbookDir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // 用户区已有（含改过的），不覆盖
		}
		raw, err := os.ReadFile(filepath.Join(seedDir, e.Name()))
		if err != nil {
			continue
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			log.Printf("[ansible] 种子 playbook 落盘失败 %s: %v", e.Name(), err)
			continue
		}
		copied++
	}
	if copied > 0 {
		log.Printf("[ansible] 已落盘 %d 个种子 playbook → %s", copied, playbookDir)
	}
}

// playbookMeta 列表条目：头部机器可读行解析（# vmops-playbook: name=x | desc=y | targets=z）。
type playbookMeta struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Desc      string `json:"desc"`
	Targets   string `json:"targets"`
	BuiltIn   bool   `json:"built_in"`
	SizeBytes int64  `json:"size_bytes"`
	UpdatedAt string `json:"updated_at"`
}

// parsePlaybookHeader 读文件头部找 vmops-playbook 元数据行。
func parsePlaybookHeader(raw []byte) (name, desc, targets string) {
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if i > 8 {
			break
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "# vmops-playbook:") {
			continue
		}
		for _, part := range strings.Split(strings.TrimPrefix(line, "# vmops-playbook:"), "|") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) != 2 {
				continue
			}
			switch strings.TrimSpace(kv[0]) {
			case "name":
				name = strings.TrimSpace(kv[1])
			case "desc":
				desc = strings.TrimSpace(kv[1])
			case "targets":
				targets = strings.TrimSpace(kv[1])
			}
		}
		break
	}
	return
}

// ListPlaybooks GET /api/ansible/playbooks —— playbook 清单（按更新时间倒序）。
func (h *AnsibleHandler) ListPlaybooks(c *gin.Context) {
	entries, err := os.ReadDir(playbookDir)
	if err != nil {
		Success(c, gin.H{"items": []playbookMeta{}})
		return
	}
	items := []playbookMeta{}
	for _, e := range entries {
		if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".yml" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".yml")
		full := filepath.Join(playbookDir, e.Name())
		raw, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		info, _ := e.Info()
		name, desc, targets := parsePlaybookHeader(raw)
		if name == "" {
			name = id
		}
		items = append(items, playbookMeta{
			ID: id, Name: name, Desc: desc, Targets: targets,
			BuiltIn: isSeedFile(e.Name()), SizeBytes: info.Size(),
			UpdatedAt: info.ModTime().Format(time.DateTime),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt > items[j].UpdatedAt })
	Success(c, gin.H{"items": items, "total": len(items)})
}

// isSeedFile 判断文件是否与仓库内置种子同名（仅用于 UI 标识，不影响权限）。
func isSeedFile(name string) bool {
	_, err := os.Stat(filepath.Join(seedDir, name))
	return err == nil
}

// GetPlaybook GET /api/ansible/playbooks/:id —— 原文（编辑器用）。
func (h *AnsibleHandler) GetPlaybook(c *gin.Context) {
	id := c.Param("id")
	if !playbookIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "playbook ID 非法")
		return
	}
	raw, err := os.ReadFile(filepath.Join(playbookDir, id+".yml"))
	if err != nil {
		Fail(c, http.StatusNotFound, "playbook 不存在")
		return
	}
	Success(c, gin.H{"id": id, "content": string(raw)})
}

// CreatePlaybook POST /api/ansible/playbooks —— 新建（先语法校验后落盘）。
// body: {id*, content*}；content 头部建议带 # vmops-playbook: 元数据行。
func (h *AnsibleHandler) CreatePlaybook(c *gin.Context) {
	var req struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数格式非法")
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if !playbookIDRe.MatchString(req.ID) {
		Fail(c, http.StatusBadRequest, "ID 仅允许字母数字与 -_（≤64 字符，字母开头）")
		return
	}
	dst := filepath.Join(playbookDir, req.ID+".yml")
	if _, err := os.Stat(dst); err == nil {
		Fail(c, http.StatusConflict, "同名 playbook 已存在")
		return
	}
	if err := ansible.SyntaxCheck(req.Content); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.WriteFile(dst, []byte(req.Content), 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "保存失败", err)
		return
	}
	Success(c, gin.H{"id": req.ID, "saved": true})
}

// UpdatePlaybook PUT /api/ansible/playbooks/:id —— 覆盖更新（先语法校验）。
func (h *AnsibleHandler) UpdatePlaybook(c *gin.Context) {
	id := c.Param("id")
	if !playbookIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "playbook ID 非法")
		return
	}
	dst := filepath.Join(playbookDir, id+".yml")
	if _, err := os.Stat(dst); err != nil {
		Fail(c, http.StatusNotFound, "playbook 不存在")
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数格式非法")
		return
	}
	if err := ansible.SyntaxCheck(req.Content); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.WriteFile(dst, []byte(req.Content), 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "保存失败", err)
		return
	}
	Success(c, gin.H{"id": id, "saved": true})
}

// DeletePlaybook DELETE /api/ansible/playbooks/:id。
func (h *AnsibleHandler) DeletePlaybook(c *gin.Context) {
	id := c.Param("id")
	if !playbookIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "playbook ID 非法")
		return
	}
	if err := os.Remove(filepath.Join(playbookDir, id+".yml")); err != nil {
		Fail(c, http.StatusNotFound, "playbook 不存在")
		return
	}
	Success(c, gin.H{"id": id, "deleted": true})
}

// CheckPlaybook POST /api/ansible/playbooks/check —— 只校验不保存（编辑器按钮）。
func (h *AnsibleHandler) CheckPlaybook(c *gin.Context) {
	var req struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数格式非法")
		return
	}
	if err := ansible.SyntaxCheck(req.Content); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"valid": true})
}

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

// Run POST /api/ansible/run —— 批量执行（operator+，路由组已挡）。
// adhoc：body {targets*[vm_id], module*(ping|command|shell), args?}
// playbook：body {targets*[vm_id], playbook*(id)}（S2）
func (h *AnsibleHandler) Run(c *gin.Context) {
	var req struct {
		Targets  []uint `json:"targets"`
		Module   string `json:"module"`
		Args     string `json:"args"`
		Playbook string `json:"playbook"`
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
	// 轻校验目标存在性（权威校验在 executor：运行态/IP/凭据逐台复核）
	var cnt int64
	if err := h.DB.Model(&model.VM{}).Where("id IN ?", req.Targets).Count(&cnt).Error; err != nil || cnt != int64(len(req.Targets)) {
		Fail(c, http.StatusBadRequest, "部分目标虚拟机不存在")
		return
	}

	payload := map[string]interface{}{"targets": req.Targets}
	var title string
	if req.Playbook != "" {
		// playbook 分支：id 合法性 + 文件存在（executor 再按 path 执行）
		if !playbookIDRe.MatchString(req.Playbook) {
			Fail(c, http.StatusBadRequest, "playbook ID 非法")
			return
		}
		if _, err := os.Stat(filepath.Join(playbookDir, req.Playbook+".yml")); err != nil {
			Fail(c, http.StatusNotFound, "playbook 不存在")
			return
		}
		payload["playbook"] = req.Playbook
		title = "Ansible playbook " + req.Playbook + " → " + strconv.Itoa(len(req.Targets)) + " 台"
	} else {
		req.Module = strings.TrimSpace(req.Module)
		if !ansibleModules[req.Module] {
			Fail(c, http.StatusBadRequest, "模块仅支持 ping / command / shell")
			return
		}
		if req.Module != "ping" && strings.TrimSpace(req.Args) == "" {
			Fail(c, http.StatusBadRequest, "模块 "+req.Module+" 需要填写执行参数")
			return
		}
		payload["module"] = req.Module
		payload["args"] = req.Args
		title = "Ansible " + req.Module + " → " + strconv.Itoa(len(req.Targets)) + " 台"
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("ansible_run", title, payload, userID, username, "", nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	// 留痕：谁、从哪、对几台机器跑了什么（shell 参数/playbook 内容不进日志——可能含敏感内容）
	log.Printf("[ansible] 提交批量执行 playbook=%q module=%q targets=%d user=%s from=%s",
		req.Playbook, req.Module, len(req.Targets), username, c.ClientIP())
	Accepted(c, "批量执行任务已提交", gin.H{"task_id": task.ID})
}
