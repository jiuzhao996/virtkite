// stacks.go：声明式部署栈（P2A，2026-10-04）——仓库 stacks/*.yml 为栈目录，
// 部署 = 复制到 data/stacks/<id>/docker-compose.yml 后 compose up -d。
// 项目名（=目录名）与容器页「编排」tab 的 -p 管理闭环：栈部署后即可在那里停止/下线。
//
// GET  /api/stacks          栈清单（含部署状态：compose ls 比对项目名）
// GET  /api/stacks/:id/docs 栈的参考笔记原文（markdown，NOTES_DIR 环境变量可配）
// POST /api/stacks/:id/deploy（admin）同步部署（拉镜像可达分钟级，dockerx 侧 10 分钟超时）
package handler

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/tasks"
)

// StackHandler 声明式部署栈处理器。Tasks 用于栈升级（stack_upgrade 异步任务）。
type StackHandler struct {
	Docker *dockerx.Dockerx
	Tasks  *tasks.Manager
}

// NewStackHandler 创建栈处理器。
func NewStackHandler(tm *tasks.Manager) *StackHandler {
	return &StackHandler{Docker: dockerx.New(), Tasks: tm}
}

// stacksDir 栈目录定位：cwd/stacks（与 locateWebRoot 同形约定，仓库根运行即命中）。
func stacksDir() string {
	return "stacks"
}

// stackMeta 从栈 YAML 头部的机器可读行解析元数据：
// # vmops-stack: id=xx | category=xx | desc=xx | docs=a.md, b.md
type stackMeta struct {
	ID       string
	Category string
	Desc     string
	Docs     []string
}

var stackMetaRe = regexp.MustCompile(`^#\s*vmops-stack:\s*(.+)$`)

func parseStackMeta(path string) (*stackMeta, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	meta := &stackMeta{ID: strings.TrimSuffix(filepath.Base(path), ".yml")}
	for _, line := range strings.Split(string(raw), "\n") {
		if m := stackMetaRe.FindStringSubmatch(line); m != nil {
			for _, kv := range strings.Split(m[1], "|") {
				k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
				if !ok {
					continue
				}
				switch k {
				case "id":
					meta.ID = v
				case "category":
					meta.Category = v
				case "desc":
					meta.Desc = v
				case "docs":
					for _, d := range strings.Split(v, ",") {
						if d = strings.TrimSpace(d); d != "" {
							meta.Docs = append(meta.Docs, d)
						}
					}
				}
			}
			break
		}
	}
	return meta, raw, nil
}

// stackServices 扫描 YAML 顶层 services 的两空格缩进键（服务名徽标用；
// 不引 yaml 依赖——compose 文件本身交给 docker 校验，这里只做展示级解析）。
func stackServices(raw []byte) []string {
	services := []string{}
	inServices := false
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "services:" {
			inServices = true
			continue
		}
		if inServices {
			if line != "" && line[0] != ' ' && line[0] != '#' && line != "services:" {
				break // 离开 services 段
			}
			if len(line) >= 3 && line[0] == ' ' && line[1] == ' ' && line[2] != ' ' {
				name := strings.TrimRight(line[2:], ":")
				if name != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
					services = append(services, name)
				}
			}
		}
	}
	return services
}

// List GET /api/stacks —— 栈清单 + 部署状态。
func (h *StackHandler) List(c *gin.Context) {
	entries, err := os.ReadDir(stacksDir())
	if err != nil {
		Success(c, gin.H{"items": []gin.H{}, "total": 0, "note": "栈目录不存在（stacks/）"})
		return
	}
	deployed := map[string]bool{}
	if projects, err := h.Docker.ComposeList(); err == nil {
		for _, p := range projects {
			deployed[p.Name] = true
		}
	}
	items := []gin.H{}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		meta, raw, err := parseStackMeta(filepath.Join(stacksDir(), e.Name()))
		if err != nil {
			continue
		}
		items = append(items, gin.H{
			"id": meta.ID, "category": meta.Category, "description": meta.Desc,
			"services": stackServices(raw), "docs": meta.Docs,
			"deployed": deployed[meta.ID],
		})
	}
	Success(c, gin.H{"items": items, "total": len(items)})
}

// Deploy POST /api/stacks/:id/deploy（admin）——复制栈文件到 data/stacks/<id>/ 并 up -d。
// id 白名单校验防路径穿越（栈 id 同时是 compose 项目名与目录名，dockerx 侧双重校验）。
func (h *StackHandler) Deploy(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "栈部署仅管理员可用")
		return
	}
	id := c.Param("id")
	if !dockerx.SafeStackID(id) {
		Fail(c, http.StatusBadRequest, "栈 ID 非法（仅允许字母数字与 -_.）")
		return
	}
	meta, raw, err := parseStackMeta(filepath.Join(stacksDir(), id+".yml"))
	if err != nil {
		Fail(c, http.StatusNotFound, "栈不存在："+id)
		return
	}
	target := filepath.Join("data", "stacks", id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "创建部署目录失败", err)
		return
	}
	if err := os.WriteFile(filepath.Join(target, "docker-compose.yml"), raw, 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "写入 compose 文件失败", err)
		return
	}
	if err := h.Docker.ComposeUpFromFile(target); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "部署失败："+err.Error(), err)
		return
	}
	Success(c, gin.H{"id": id, "deployed": true, "desc": meta.Desc})
}

// Detail GET /api/stacks/:id/detail —— 栈详情：服务列表 + 部署副本内容 + 漂移标志。
// 漂移 = data/stacks/<id>/docker-compose.yml（可编辑副本）与 stacks/<id>.yml（仓库模板）
// 字节不一致；未部署时回模板内容并置 deployed=false。
func (h *StackHandler) Detail(c *gin.Context) {
	id := c.Param("id")
	if !dockerx.SafeStackID(id) {
		Fail(c, http.StatusBadRequest, "栈 ID 非法（仅允许字母数字与 -_.）")
		return
	}
	meta, tmplRaw, err := parseStackMeta(filepath.Join(stacksDir(), id+".yml"))
	if err != nil {
		Fail(c, http.StatusNotFound, "栈不存在："+id)
		return
	}
	copyPath := filepath.Join("data", "stacks", id, "docker-compose.yml")
	copyRaw, copyErr := os.ReadFile(copyPath)
	deployed := copyErr == nil
	content := tmplRaw
	if deployed {
		content = copyRaw
	}
	drift := deployed && !bytes.Equal(bytes.TrimSpace(copyRaw), bytes.TrimSpace(tmplRaw))

	services := []interface{}{}
	if deployed {
		if list, serr := h.Docker.ComposeServices(id); serr == nil {
			for _, s := range list {
				services = append(services, s)
			}
		}
	}
	var composeStatus string
	if projects, perr := h.Docker.ComposeList(); perr == nil {
		for _, p := range projects {
			if p.Name == id {
				composeStatus = p.Status
				break
			}
		}
	}
	Success(c, gin.H{
		"id": id, "category": meta.Category, "description": meta.Desc,
		"docs": meta.Docs, "templateServices": stackServices(tmplRaw),
		"deployed": deployed, "drift": drift, "content": string(content),
		"template": string(tmplRaw), "services": services, "status": composeStatus,
	})
}

// File PUT /api/stacks/:id/file（admin）——写部署副本 data/stacks/<id>/docker-compose.yml。
// 仓库模板 stacks/*.yml 永不可经 API 写：它同时是商店目录的源数据，被写坏会污染所有
// 用户的部署入口。写前用 compose config -q 真校验，不过则 400 并回显 docker 原生报错
// （含行号，对教学场景有价值）。
func (h *StackHandler) File(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "栈文件编辑仅管理员可用")
		return
	}
	id := c.Param("id")
	if !dockerx.SafeStackID(id) {
		Fail(c, http.StatusBadRequest, "栈 ID 非法（仅允许字母数字与 -_.）")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Content) == "" {
		Fail(c, http.StatusBadRequest, "请提供 compose 文件内容")
		return
	}
	target := filepath.Join("data", "stacks", id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "创建部署目录失败", err)
		return
	}
	// 先写临时文件校验，通过后再落正式文件——避免校验期间污染正在运行的部署副本
	tmp := filepath.Join(target, ".check.yml")
	if err := os.WriteFile(tmp, []byte(body.Content), 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "写入校验文件失败", err)
		return
	}
	if err := h.Docker.ComposeConfigCheck(tmp); err != nil {
		_ = os.Remove(tmp)
		Fail(c, http.StatusBadRequest, "compose 文件校验失败："+err.Error())
		return
	}
	_ = os.Remove(tmp)
	if err := os.WriteFile(filepath.Join(target, "docker-compose.yml"), []byte(body.Content), 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "保存 compose 文件失败", err)
		return
	}
	Success(c, gin.H{"id": id, "message": "已保存，需重新部署后生效", "drift": true})
}

// Upgrade POST /api/stacks/:id/upgrade（admin）——提交 stack_upgrade 异步任务（pull+up -d）。
// 拉镜像可达数十分钟，同步挂在 HTTP 请求上正是任务管线要解决的问题（app_install 同款取舍），
// 故直接走慢池任务，前端按 TaskList 轮询进度。
func (h *StackHandler) Upgrade(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "栈升级仅管理员可用")
		return
	}
	id := c.Param("id")
	if !dockerx.SafeStackID(id) {
		Fail(c, http.StatusBadRequest, "栈 ID 非法（仅允许字母数字与 -_.）")
		return
	}
	dir := filepath.Join("data", "stacks", id)
	if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err != nil {
		Fail(c, http.StatusNotFound, "该栈尚未部署，请先部署后再升级")
		return
	}
	if h.Tasks == nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "任务系统未就绪", nil)
		return
	}
	uid, uname := taskUserFromContext(c)
	t, err := h.Tasks.Submit("stack_upgrade", "升级栈 "+id, gin.H{"stack_id": id, "dir": dir}, uid, uname, "", nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交升级任务失败", err)
		return
	}
	Success(c, gin.H{"id": id, "task_id": t.ID, "message": "升级任务已提交，可在任务中心查看进度"})
}

// Docs GET /api/stacks/:id/docs?path=xx.md —— 栈参考笔记原文（前端 markdown 渲染）。
// path 必须命中该栈元数据 docs 清单（防任意文件读取），再经 NOTES_DIR 前缀校验。
func (h *StackHandler) Docs(c *gin.Context) {
	id := c.Param("id")
	rel := c.Query("path")
	meta, _, err := parseStackMeta(filepath.Join(stacksDir(), id+".yml"))
	if err != nil {
		Fail(c, http.StatusNotFound, "栈不存在："+id)
		return
	}
	allowed := false
	for _, d := range meta.Docs {
		if d == rel {
			allowed = true
			break
		}
	}
	if !allowed || strings.Contains(rel, "..") {
		Fail(c, http.StatusBadRequest, "该笔记不在本栈的参考清单中")
		return
	}
	root := os.Getenv("NOTES_DIR")
	if root == "" {
		root = "/home/jiuzhao/data/Firefly-6.16.5/src/content/posts"
	}
	full := filepath.Join(root, filepath.Clean("/"+rel)) // Clean("/"+rel) 归一并压掉 ..
	raw, err := os.ReadFile(full)
	if err != nil {
		Fail(c, http.StatusNotFound, "笔记文件不可读（NOTES_DIR 未配置或路径不存在）")
		return
	}
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", raw)
}
