// ansible_playbooks.go：运维自动化——playbook 库（清单/CRUD/语法校验/目录内文件读写）。
// 单文件 <id>.yml 或目录形态 <id>/（role 结构）均支持；seed 与目录约定见 ansible.go。
package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/ansible"
)

// playbookMeta 列表条目：头部机器可读行解析（# vmops-playbook: name=x | desc=y | targets=z）。
type playbookMeta struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Desc    string   `json:"desc"`
	Targets string   `json:"targets"`
	Vars    []string `json:"vars,omitempty"`
	// Survey 带类型的执行参数定义（survey 块优先；无块时由 vars= 清单按 str 兜底）。
	// 前端执行对话框按它渲染控件（下拉/密码/数字/多行），不再只有裸输入框。
	Survey    []ansible.SurveyVar `json:"survey,omitempty"`
	BuiltIn   bool                `json:"built_in"`
	IsDir     bool                `json:"is_dir"` // 目录形态（role 结构：<id>/site.yml + roles/ templates/ …）
	SizeBytes int64               `json:"size_bytes"`
	UpdatedAt string              `json:"updated_at"`
}

// playbookSurvey 组装清单/执行用的参数定义：优先解析头部 survey 块；没有则把旧的
// `vars=a,b` 逗号清单按 str 兜底（向后兼容：旧 playbook 一行不改照常可用）。
// 解析失败（块写坏了）不阻断列表——回落 vars= 兜底，等保存/执行时再报明确错误。
func playbookSurvey(raw []byte, headerVars []string) []ansible.SurveyVar {
	if sv, present, err := ansible.ParseSurvey(raw); err == nil && present {
		return sv
	}
	out := make([]ansible.SurveyVar, 0, len(headerVars))
	for _, v := range headerVars {
		out = append(out, ansible.SurveyVar{Name: v, Type: ansible.SurveyTypeStr})
	}
	return out
}

// validatePlaybookSurvey 解析并强校验内容里的 survey 定义（保存/语法校验入口调用）。
// 无 survey 块 = 合法（走旧 vars= 语义）；块存在但定义非法 → 返回中文错误，拒绝保存。
func validatePlaybookSurvey(content string) error {
	sv, present, err := ansible.ParseSurvey([]byte(content))
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	return ansible.ValidateSurvey(sv)
}

// parsePlaybookHeader 读文件头部找 vmops-playbook 元数据行（只看前 8 行）。
// vars 逗号分隔的变量名清单（S4 vars 表单化约定）：执行对话框按它渲染输入框，
// 值经 -e JSON 注入，优先级高于 playbook 内 vars 段。
func parsePlaybookHeader(raw []byte) (name, desc, targets string, vars []string) {
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if i > 7 {
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
			case "vars":
				for _, v := range strings.Split(strings.TrimSpace(kv[1]), ",") {
					if v = strings.TrimSpace(v); v != "" {
						vars = append(vars, v)
					}
				}
			}
		}
		break
	}
	return
}

// ListPlaybooks GET /api/ansible/playbooks —— playbook 清单（按更新时间倒序）。
func (h *AnsibleHandler) ListPlaybooks(c *gin.Context) {
	entries, err := os.ReadDir(playbookDir())
	if err != nil {
		Success(c, gin.H{"items": []playbookMeta{}})
		return
	}
	items := []playbookMeta{}
	for _, e := range entries {
		// 单文件 <id>.yml 或目录 <id>/（role 形态，主文件 site.yml）
		isDir := e.IsDir()
		var id, mainFile string
		if isDir {
			id = e.Name()
			p, _, ok := ansible.ResolvePlaybook(playbookDir(), id)
			if !ok {
				continue // 空目录（无任何 yml）跳过
			}
			mainFile = p
		} else {
			if strings.ToLower(filepath.Ext(e.Name())) != ".yml" {
				continue
			}
			id = strings.TrimSuffix(e.Name(), ".yml")
			mainFile = filepath.Join(playbookDir(), e.Name())
		}
		raw, err := os.ReadFile(mainFile)
		if err != nil {
			continue
		}
		info, _ := e.Info()
		name, desc, targets, pvars := parsePlaybookHeader(raw)
		if name == "" {
			name = id
		}
		items = append(items, playbookMeta{
			ID: id, Name: name, Desc: desc, Targets: targets, Vars: pvars,
			Survey:  playbookSurvey(raw, pvars),
			BuiltIn: isSeedFile(e.Name()), IsDir: isDir, SizeBytes: info.Size(),
			UpdatedAt: info.ModTime().Format(time.DateTime),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt > items[j].UpdatedAt })
	Success(c, gin.H{"items": items, "total": len(items)})
}

// GetPlaybook GET /api/ansible/playbooks/:id —— 原文（编辑器用）。
func (h *AnsibleHandler) GetPlaybook(c *gin.Context) {
	id := c.Param("id")
	if !playbookIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "playbook ID 非法")
		return
	}
	p, _, ok := ansible.ResolvePlaybook(playbookDir(), id)
	if !ok {
		Fail(c, http.StatusNotFound, "playbook 不存在")
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		Fail(c, http.StatusNotFound, "playbook 不存在")
		return
	}
	Success(c, gin.H{"id": id, "content": string(raw), "is_dir": ansible.IsDirPlaybook(playbookDir(), id)})
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
	dst := filepath.Join(playbookDir(), req.ID+".yml")
	if _, err := os.Stat(dst); err == nil {
		Fail(c, http.StatusConflict, "同名 playbook 已存在")
		return
	}
	if err := ansible.SyntaxCheck(req.Content); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePlaybookSurvey(req.Content); err != nil {
		Fail(c, http.StatusBadRequest, "执行参数定义有误："+err.Error())
		return
	}
	if err := os.WriteFile(dst, []byte(req.Content), 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "保存失败", err)
		return
	}
	Success(c, gin.H{"id": req.ID, "saved": true})
}

// UpdatePlaybook PUT /api/ansible/playbooks/:id —— 覆盖更新（先语法校验）。
// 目录形态写回主文件（site.yml），不会在目录旁另建 <id>.yml。
func (h *AnsibleHandler) UpdatePlaybook(c *gin.Context) {
	id := c.Param("id")
	if !playbookIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "playbook ID 非法")
		return
	}
	dst, isDir, ok := ansible.ResolvePlaybook(playbookDir(), id)
	if !ok {
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
	// 目录形态：整目录可见才能校验 roles 相对引用；单文件：临时单文件校验
	var verr error
	if isDir {
		verr = ansible.SyntaxCheckContentInDir(dst, req.Content)
	} else {
		verr = ansible.SyntaxCheck(req.Content)
	}
	if verr != nil {
		Fail(c, http.StatusBadRequest, verr.Error())
		return
	}
	if err := validatePlaybookSurvey(req.Content); err != nil {
		Fail(c, http.StatusBadRequest, "执行参数定义有误："+err.Error())
		return
	}
	if err := os.WriteFile(dst, []byte(req.Content), 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "保存失败", err)
		return
	}
	Success(c, gin.H{"id": id, "saved": true})
}

// DeletePlaybook DELETE /api/ansible/playbooks/:id（单文件 <id>.yml 或目录 <id>/）。
func (h *AnsibleHandler) DeletePlaybook(c *gin.Context) {
	id := c.Param("id")
	if !playbookIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "playbook ID 非法")
		return
	}
	single := filepath.Join(playbookDir(), id+".yml")
	dir := filepath.Join(playbookDir(), id)
	// 存在性判定必须显式做：os.RemoveAll 对不存在的路径返回 nil，
	// 直接调会把「删不存在的 playbook」变成 200 假成功。
	if _, err := os.Stat(single); err == nil {
		if err := os.Remove(single); err != nil {
			ErrorWithMessage(c, http.StatusInternalServerError, "删除失败", err)
			return
		}
		Success(c, gin.H{"id": id, "deleted": true})
		return
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		Fail(c, http.StatusNotFound, "playbook 不存在")
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "删除失败", err)
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
	if err := validatePlaybookSurvey(req.Content); err != nil {
		Fail(c, http.StatusBadRequest, "执行参数定义有误："+err.Error())
		return
	}
	Success(c, gin.H{"valid": true})
}

// ListPlaybookFiles GET /api/ansible/playbooks/:id/files —— 目录形态 playbook 的文件清单（递归）。
func (h *AnsibleHandler) ListPlaybookFiles(c *gin.Context) {
	files, err := ansible.ListDirFiles(playbookDir(), c.Param("id"))
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"items": files})
}

// ReadPlaybookFile GET /api/ansible/playbooks/:id/file?path=roles/x/tasks/main.yml
func (h *AnsibleHandler) ReadPlaybookFile(c *gin.Context) {
	content, err := ansible.ReadDirFile(playbookDir(), c.Param("id"), c.Query("path"))
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"content": content})
}

// WritePlaybookFile PUT /api/ansible/playbooks/:id/file body {path, content}
// 写后对目录做一次语法校验（roles 相对引用需整目录可见）；校验失败仍保存，但回 warning 提示。
// 校验目标 = 刚写的那个文件（若它是 yml/yaml 入口），否则退回主文件——否则写坏一个副入口
// playbook 会静默通过，等执行时才炸。
func (h *AnsibleHandler) WritePlaybookFile(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		Fail(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	if err := ansible.WriteDirFile(playbookDir(), id, req.Path, req.Content); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	warning := ""
	if main, _, ok := ansible.ResolvePlaybook(playbookDir(), id); ok {
		target := main
		isYml := false
		if ext := strings.ToLower(filepath.Ext(req.Path)); ext == ".yml" || ext == ".yaml" {
			// 写的是入口文件：校验它本身（路径已被 WritePlaybookFile 的守卫校验过）
			target = filepath.Join(playbookDir(), id, filepath.FromSlash(req.Path))
			isYml = true
		}
		if err := ansible.SyntaxCheckFileInDir(main, target, req.Content); err != nil {
			warning = err.Error()
		}
		// 主文件的 survey 定义也要校验：这里是绕过「保存前校验」的第二条写入口，
		// 定义写坏会让执行对话框失效（与语法校验同口径：不阻断保存，回 warning）。
		if isYml && target == main {
			if verr := validatePlaybookSurvey(req.Content); verr != nil {
				if warning != "" {
					warning += "；"
				}
				warning += "执行参数定义有误：" + verr.Error()
			}
		}
	}
	Success(c, gin.H{"saved": true, "warning": warning})
}

// UploadPlaybookFile POST /api/ansible/playbooks/:id/upload（multipart：file + path=相对路径）
func (h *AnsibleHandler) UploadPlaybookFile(c *gin.Context) {
	id := c.Param("id")
	rel := strings.TrimSpace(c.PostForm("path"))
	fh, err := c.FormFile("file")
	if err != nil {
		Fail(c, http.StatusBadRequest, "缺少上传文件")
		return
	}
	if rel == "" {
		rel = fh.Filename
	}
	src, err := fh.Open()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取上传文件失败", err)
		return
	}
	defer src.Close()
	n, err := ansible.SaveDirUpload(playbookDir(), id, rel, src)
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已上传", "size": n})
}

// DeletePlaybookFile DELETE /api/ansible/playbooks/:id/file?path=
func (h *AnsibleHandler) DeletePlaybookFile(c *gin.Context) {
	if err := ansible.DeleteDirFile(playbookDir(), c.Param("id"), c.Query("path")); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已删除"})
}
