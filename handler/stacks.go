// stacks.go：声明式部署栈（P2A，2026-10-04）——仓库 stacks/*.yml 为栈目录，
// 部署 = 复制到 data/stacks/<id>/docker-compose.yml 后 compose up -d。
// 项目名（=目录名）与容器页「编排」tab 的 -p 管理闭环：栈部署后即可在那里停止/下线。
//
// GET  /api/stacks          栈清单（含部署状态：compose ls 比对项目名）
// GET  /api/stacks/:id/docs 栈的参考笔记原文（markdown，NOTES_DIR 环境变量可配）
// GET  /api/stacks/:id/versions 版本历史（部署/升级/回滚的 compose 快照）
// POST /api/stacks/:id/deploy（admin）同步部署（拉镜像可达分钟级，dockerx 侧 10 分钟超时）
// POST /api/stacks/:id/rollback（admin）回滚到指定历史版本（异步任务，复用升级管线）
package handler

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/database"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/tasks"
	"gorm.io/gorm"
)

// StackHandler 声明式部署栈处理器。
//   - Tasks 用于栈升级 / 回滚（stack_upgrade / stack_rollback 异步任务）；
//   - DB 用于版本历史（Deploy 成功后记快照、回滚目标校验）。
type StackHandler struct {
	Docker *dockerx.Dockerx
	Tasks  *tasks.Manager
	DB     *gorm.DB
}

// NewStackHandler 创建栈处理器。
// DB 回落到 main 启动时初始化的进程级 DB（本批次 routes.go 未接线，禁止改动该文件）；
// 一旦 routes.go 改为 NewStackHandler(deps.Tasks, deps.DB)，此回落分支可删。
func NewStackHandler(tm *tasks.Manager) *StackHandler {
	return &StackHandler{Docker: dockerx.New(), Tasks: tm, DB: database.GetDB()}
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

// stackMetaRe 匹配栈 YAML 头部那条机器可读元数据行（形如 `# vmops-stack: id=xx | ...`）。
// 用注释行而非真 YAML 键：compose 文件本身仍可被 docker 直接消费，栈目录也顺带充当
// 商店目录，无需额外的清单文件。
var stackMetaRe = regexp.MustCompile(`^#\s*vmops-stack:\s*(.+)$`)

// parseStackMeta 读取栈 YAML 并解析头部元数据行，返回元数据与文件原文。
// 只认首条匹配行；元数据缺失时 ID 回落到文件名（去掉 .yml）。刻意不引 yaml 依赖——
// compose 文件的语法合法性交给 docker 校验，这里只做展示级解析。
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
	target := config.DataPath("stacks", id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "创建部署目录失败", err)
		return
	}
	uid, uname := taskUserFromContext(c)
	// 部署会用仓库模板覆盖部署副本：先把「被覆盖的旧内容」存档（存在且与最新版本不同才新增），
	// 保证用户手工改过、尚未升级的内容不会因一次「重新部署」而永久丢失。非致命：快照失败
	// 不阻断部署，仅留痕。
	if old, rerr := os.ReadFile(filepath.Join(target, "docker-compose.yml")); rerr == nil {
		if _, _, err := tasks.RecordStackVersion(h.DB, id, string(old), "部署前快照", uname, uid, nil); err != nil {
			LogError(c, err)
		}
	}
	if err := os.WriteFile(filepath.Join(target, "docker-compose.yml"), raw, 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "写入 compose 文件失败", err)
		return
	}
	if err := h.Docker.ComposeUpFromFile(target); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "部署失败："+err.Error(), err)
		return
	}
	// 部署成功后把本次生效的编排内容记入历史（去重：内容没变则不新增）。
	// 记录失败不判部署失败（部署本身已成功），但必须留痕（AGENTS：DB 写失败禁止静默吞），
	// 并把标记回给前端。raw 即刚写入的模板内容。
	_, _, recErr := tasks.RecordStackVersion(h.DB, id, string(raw), "部署", uname, uid, nil)
	if recErr != nil {
		LogError(c, recErr)
	}
	Success(c, gin.H{"id": id, "deployed": true, "desc": meta.Desc, "version_recorded": recErr == nil})
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
	copyPath := config.DataPath("stacks", id, "docker-compose.yml")
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
	target := config.DataPath("stacks", id)
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
	dir := config.DataPath("stacks", id)
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

// Versions GET /api/stacks/:id/versions —— 栈的 compose 版本历史（含内容，供前端「查看内容」直显）。
// 每次成功部署/升级/回滚记一条快照，最多保留最近 N 个（N 见 service/tasks 的 stackVersionKeepN）。
func (h *StackHandler) Versions(c *gin.Context) {
	id := c.Param("id")
	if !dockerx.SafeStackID(id) {
		Fail(c, http.StatusBadRequest, "栈 ID 非法（仅允许字母数字与 -_.）")
		return
	}
	items, err := tasks.ListStackVersions(h.DB, id)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取版本历史失败", err)
		return
	}
	Success(c, gin.H{"items": items, "total": len(items)})
}

// Rollback POST /api/stacks/:id/rollback（admin）——提交 stack_rollback 异步任务：
// 把指定历史版本内容写回部署副本并重新起栈（复用 stack_upgrade 的 pull + up 管线）。
// body：{version_id}（stack_versions 行 id）。
func (h *StackHandler) Rollback(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "栈回滚仅管理员可用")
		return
	}
	id := c.Param("id")
	if !dockerx.SafeStackID(id) {
		Fail(c, http.StatusBadRequest, "栈 ID 非法（仅允许字母数字与 -_.）")
		return
	}
	dir := config.DataPath("stacks", id)
	if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err != nil {
		Fail(c, http.StatusNotFound, "该栈尚未部署，无法回滚")
		return
	}
	var body struct {
		VersionID uint `json:"version_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.VersionID == 0 {
		Fail(c, http.StatusBadRequest, "请指定回滚目标版本")
		return
	}
	// 目标版本必须存在且属于本栈（executor 侧会再校一次，此处先给即时反馈）。
	ver, err := tasks.GetStackVersion(h.DB, body.VersionID)
	if err != nil || ver.StackID != id {
		Fail(c, http.StatusNotFound, "回滚目标版本不存在或不属于该栈")
		return
	}
	if h.Tasks == nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "任务系统未就绪", nil)
		return
	}
	// 栈级锁预检：与 composeWriteErr 同语义，冲突即时 409（真正串行化由任务内持锁调用完成）。
	if h.Docker.IsStackBusy(id) {
		Fail(c, http.StatusConflict, "该栈正在执行其他操作，请稍后再试")
		return
	}
	uid, uname := taskUserFromContext(c)
	t, err := h.Tasks.Submit("stack_rollback", fmt.Sprintf("回滚栈 %s 到 v%d", id, ver.Version),
		gin.H{"stack_id": id, "dir": dir, "version_id": body.VersionID}, uid, uname, "", nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交回滚任务失败", err)
		return
	}
	Success(c, gin.H{
		"id": id, "task_id": t.ID,
		"message": fmt.Sprintf("回滚任务已提交（目标 v%d），可在任务中心查看进度", ver.Version),
	})
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
