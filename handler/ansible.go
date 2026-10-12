// ansible.go：运维自动化（P4 S1）——引擎状态探测 + adhoc 批量执行入口。
// 目标只收 vm_id（服务端查库取 IP/凭据，白名单结构性成立）；执行走 ansible_run
// 异步任务，输出随任务 Result 实时刷新（前端轮询任务详情）。
package handler

import (
	"fmt"
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
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/ansible"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/vmssh"
	"gorm.io/gorm"
)

// AnsibleHandler 运维自动化处理器。
type AnsibleHandler struct {
	DB           *gorm.DB
	Tasks        *tasks.Manager
	MasterSecret string // 凭据主密钥（分发公钥时解密托管口令；与 VMCredentialHandler 同源）
}

// NewAnsibleHandler 创建处理器。
func NewAnsibleHandler(db *gorm.DB, taskMgr *tasks.Manager, masterSecret string) *AnsibleHandler {
	return &AnsibleHandler{DB: db, Tasks: taskMgr, MasterSecret: masterSecret}
}

// 允许的 adhoc 模块：ping/command/shell 之外，补上教学与运维最常用的幂等模块
// （facts 采集、文件/用户/包/服务管理、cron），让「快速执行」页能直观演示幂等。
// 本来就是运维场景（root 批量执行），allowlist 收敛的是「误用面」而非权限面。
var ansibleModules = map[string]bool{
	"ping": true, "command": true, "shell": true,
	"setup": true, // facts 采集（可看发行版/内核/内存等）
	"file":  true, "copy": true, "lineinfile": true, "stat": true,
	"user": true, "group": true,
	"package": true, "yum": true, "dnf": true, "apt": true,
	"service": true, "systemd": true, "cron": true,
	"get_url": true,
}

// ansibleModuleList 模块白名单的展示文案（错误提示用，保持与上表同步）。
const ansibleModuleList = "ping / command / shell / setup / file / copy / lineinfile / stat / user / group / package / yum / dnf / apt / service / systemd / cron / get_url"

// playbook 目录约定：仓库内置种子（git 跟踪，ansible/playbooks/）首次启动复制到
// data/ansible/playbooks/（用户编辑区，重启不覆盖）；id = 文件名去 .yml。
var playbookIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// roleDir 共享 role 库（data/ansible/roles/）——任何 playbook 都能 `roles: [<名>]` 引用，
// 不必各自复制一份。执行时经 run 级 ansible.cfg 的 roles_path 可见（见 tasks 侧），
// 保存前 --syntax-check 经 ANSIBLE_ROLES_PATH 可见（见 ansible.execCombined）。
func roleDir() string { return config.DataPath("ansible", "roles") }

// seedRoleDir 仓库内置 role 种子源（git 跟踪）。
func seedRoleDir() string { return filepath.Join("ansible", "roles") }

// EnsureSeedRoles 共享 role 种子落盘：缺哪个补哪个，用户改过的不覆盖（与 playbook 同语义）。
// 同时把共享 role 库路径注入 ansible 包——语法校验与执行必须用同一个值，否则会出现
// 「保存时校验不过、执行时却能跑」的割裂。
func EnsureSeedRoles() {
	rolesPath := roleDir()
	// 绝对化后再注入：这个值会写进交给 ansible 子进程的 ANSIBLE_ROLES_PATH / roles_path，
	// 而 ansible 对配置里的相对路径是按「配置文件所在目录」解析的（实测：roles_path
	// 相对值被解析成 <run 目录>/data/ansible/roles，role 直接找不到）。
	if abs, err := filepath.Abs(rolesPath); err == nil {
		rolesPath = abs
	}
	ansible.SharedRolesDir = rolesPath
	if err := os.MkdirAll(rolesPath, 0o755); err != nil {
		log.Printf("[ansible] 创建 role 库目录失败: %v", err)
		return
	}
	entries, err := os.ReadDir(seedRoleDir())
	if err != nil {
		return // 仓库内无种子目录（非源码运行），静默跳过
	}
	copied := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dst := filepath.Join(rolesPath, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := copySeedTree(filepath.Join(seedRoleDir(), e.Name()), dst); err != nil {
			log.Printf("[ansible] 种子 role 落盘失败 %s: %v", e.Name(), err)
			continue
		}
		copied++
	}
	if copied > 0 {
		log.Printf("[ansible] 已落盘 %d 个种子 role → %s", copied, rolesPath)
	}
}

// playbookDir 用户编辑区（运行数据，随 DATA_DIR）——函数而非包级 var：
// 相对路径会随工作目录漂移，且 var 在 init 期即固化，无法随配置/测试覆盖生效。
func playbookDir() string { return config.DataPath("ansible", "playbooks") }

// seedPlaybookDir 仓库内置种子源（git 跟踪的只读创作内容，保持仓库根相对路径）。
func seedPlaybookDir() string { return filepath.Join("ansible", "playbooks") }

// EnsureSeedPlaybooks 种子落盘：目录里缺哪个补哪个（用户删掉的会复活——种子语义
// 就是「出厂预设」；用户改过的同名文件不覆盖）。routes.go 装配时调用一次。
func EnsureSeedPlaybooks() {
	if err := os.MkdirAll(playbookDir(), 0o755); err != nil {
		log.Printf("[ansible] 创建 playbook 目录失败: %v", err)
		return
	}
	entries, err := os.ReadDir(seedPlaybookDir())
	if err != nil {
		return // 仓库内无种子目录（非源码运行），静默跳过
	}
	copied := 0
	for _, e := range entries {
		dst := filepath.Join(playbookDir(), e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // 用户区已有（含改过的），不覆盖
		}
		if e.IsDir() {
			// 目录形态种子（role 结构：site.yml + roles/ templates/ …），整树复制
			if err := copySeedTree(filepath.Join(seedPlaybookDir(), e.Name()), dst); err != nil {
				log.Printf("[ansible] 种子 playbook 目录落盘失败 %s: %v", e.Name(), err)
				continue
			}
			copied++
			continue
		}
		if strings.ToLower(filepath.Ext(e.Name())) != ".yml" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(seedPlaybookDir(), e.Name()))
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
		log.Printf("[ansible] 已落盘 %d 个种子 playbook → %s", copied, playbookDir())
	}
}

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

// isSeedFile 判断文件是否与仓库内置种子同名（仅用于 UI 标识，不影响权限）。
func isSeedFile(name string) bool {
	_, err := os.Stat(filepath.Join(seedPlaybookDir(), name))
	return err == nil
}

// copySeedTree 递归复制种子目录（目录 0755 / 文件 0644）。
func copySeedTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, b, 0o644)
	})
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

// Status GET /api/ansible/status —— 引擎探测 + 免密态势（P4-S4）。
func (h *AnsibleHandler) Status(c *gin.Context) {
	eng, err := ansible.Detect()
	if err != nil {
		Success(c, gin.H{
			"installed": false,
			"hint":      "宿主机未安装 ansible（pip install --user ansible 或系统包管理器安装后刷新）",
		})
		return
	}
	out := gin.H{
		"installed": true,
		"path":      eng.PlaybookPath,
		"version":   eng.Version,
	}
	// 免密态势：平台密钥指纹 + 已注入 VM 数 / 已托管凭据 VM 数（分发的候选池）
	if _, pub, kerr := ansible.EnsureKeyPair(config.DataPath("ansible")); kerr == nil {
		out["key_fingerprint"] = ansible.FingerprintPair(pub)
		out["pubkey"] = pub
	}
	var readyCnt, credCnt int64
	h.DB.Model(&model.VM{}).Where("ansible_ready = ?", true).Count(&readyCnt)
	h.DB.Model(&model.VMCredential{}).Count(&credCnt)
	out["ansible_ready_count"] = readyCnt
	out["cred_count"] = credCnt
	// per-VM 就绪标记（AU1）：目标选择处标注「无凭据」机器，选中即失败的坑前置暴露
	readyIDs := []uint{}
	h.DB.Model(&model.VM{}).Where("ansible_ready = ?", true).Pluck("id", &readyIDs)
	credIDs := []uint{}
	h.DB.Model(&model.VMCredential{}).Pluck("vm_id", &credIDs)
	out["ansible_ready_ids"] = readyIDs
	out["cred_vm_ids"] = credIDs
	Success(c, out)
}

// DeployKey POST /api/ansible/deploy-key —— 平台公钥分发（存量 VM 补注入）。
// targets 可省：默认全部「已托管凭据且未免密」的 VM。逐台经现有凭据通道 SSH
// 注入 authorized_key（grep -qxF 幂等，不重复写），成功即标记 ansible_ready。
func (h *AnsibleHandler) DeployKey(c *gin.Context) {
	var req struct {
		Targets []uint `json:"targets"`
	}
	_ = c.ShouldBindJSON(&req)
	if h.MasterSecret == "" {
		Fail(c, http.StatusServiceUnavailable, "凭据加密未初始化，请联系管理员配置主密钥后重启服务")
		return
	}
	_, pub, err := ansible.EnsureKeyPair(config.DataPath("ansible"))
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "平台密钥不可用", err)
		return
	}
	q := h.DB.Model(&model.VM{}).Where("ansible_ready = ?", false)
	if len(req.Targets) > 0 {
		q = q.Where("id IN ?", req.Targets)
	}
	var vms []model.VM
	if err := q.Find(&vms).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取虚拟机失败", err)
		return
	}
	if len(vms) == 0 {
		Success(c, gin.H{"ok": 0, "skipped": 0, "failed": []gin.H{}, "message": "没有需要分发的虚拟机（全部已免密或无目标）"})
		return
	}
	// 只处理有托管凭据的（无凭据的连口令通道都没有，无从注入）
	ids := make([]uint, 0, len(vms))
	for _, vm := range vms {
		ids = append(ids, vm.ID)
	}
	var creds []model.VMCredential
	h.DB.Where("vm_id IN ?", ids).Find(&creds)
	credByVM := map[uint]model.VMCredential{}
	for _, c := range creds {
		credByVM[c.VMID] = c
	}

	okCnt, skipCnt := 0, 0
	failed := []gin.H{}
	for _, vm := range vms {
		cred, has := credByVM[vm.ID]
		if !has {
			skipCnt++
			failed = append(failed, gin.H{"name": vm.Name, "reason": "未保存托管凭据"})
			continue
		}
		port := cred.Port
		if port <= 0 {
			port = 22
		}
		plain, derr := secretbox.OpenWithMaster(h.MasterSecret, cred.Salt, cred.PasswordEnc)
		if derr != nil {
			failed = append(failed, gin.H{"name": vm.Name, "reason": "凭据解密失败"})
			continue
		}
		opt, oerr := vmssh.NewOptions(vm.IP, vm.IP, port, cred.User, string(plain))
		if oerr != nil {
			failed = append(failed, gin.H{"name": vm.Name, "reason": "目标未通过安全校验"})
			continue
		}
		// 幂等注入：已存在同款公钥（整行精确匹配）不重复写
		cmd := fmt.Sprintf("mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && grep -qxF %q ~/.ssh/authorized_keys || echo %q >> ~/.ssh/authorized_keys; chmod 600 ~/.ssh/authorized_keys", pub, pub)
		if _, _, rerr := vmssh.Run(opt, cmd, 30*time.Second); rerr != nil {
			failed = append(failed, gin.H{"name": vm.Name, "reason": "SSH 注入失败"})
			continue
		}
		if uerr := h.DB.Model(&model.VM{}).Where("id = ?", vm.ID).
			Updates(map[string]interface{}{"ansible_ready": true, "ansible_user": cred.User}).Error; uerr != nil {
			log.Printf("[ansible] 标记 ansible_ready 失败 vm=%s: %v", vm.Name, uerr)
		}
		okCnt++
	}
	log.Printf("[ansible] 公钥分发完成 ok=%d skip=%d failed=%d from=%s", okCnt, skipCnt, len(failed), c.ClientIP())
	Success(c, gin.H{"ok": okCnt, "skipped": skipCnt, "failed": failed})
}

// Run POST /api/ansible/run —— 批量执行（operator+，路由组已挡）。
// adhoc：body {targets*[vm_id], module*(ping|command|shell), args?}
// playbook：body {targets*[vm_id], playbook*(id)}（S2）
func (h *AnsibleHandler) Run(c *gin.Context) {
	var req struct {
		Targets   []uint                 `json:"targets"`
		GroupID   uint                   `json:"group_id"`
		Module    string                 `json:"module"`
		Args      string                 `json:"args"`
		Playbook  string                 `json:"playbook"`
		ExtraVars map[string]interface{} `json:"extra_vars"` // survey 答案（值可为字符串或数字）
		Check     bool                   `json:"check"`      // 预演（--check，仅 playbook）
		Diff      bool                   `json:"diff"`       // 差异（--diff，仅 playbook）
		Tags      string                 `json:"tags"`       // 只跑指定标签（-t，仅 playbook）
		SkipTags  string                 `json:"skip_tags"`  // 跳过指定标签（仅 playbook）
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数格式非法")
		return
	}
	// 目标来源：显式 targets 或主机组 group_id（至少其一；executor 侧再合并去重）
	if len(req.Targets) == 0 && req.GroupID == 0 {
		Fail(c, http.StatusBadRequest, "请选择目标虚拟机或主机组")
		return
	}
	if len(req.Targets) > 50 {
		Fail(c, http.StatusBadRequest, "单次执行最多 50 台")
		return
	}
	// 轻校验目标存在性（权威校验在 executor：运行态/IP/凭据逐台复核）
	if len(req.Targets) > 0 {
		var cnt int64
		if err := h.DB.Model(&model.VM{}).Where("id IN ?", req.Targets).Count(&cnt).Error; err != nil || cnt != int64(len(req.Targets)) {
			Fail(c, http.StatusBadRequest, "部分目标虚拟机不存在")
			return
		}
	}
	if req.GroupID > 0 {
		var g model.HostGroup
		if err := h.DB.First(&g, req.GroupID).Error; err != nil {
			Fail(c, http.StatusBadRequest, "主机组不存在")
			return
		}
	}

	targetDesc := strconv.Itoa(len(req.Targets)) + " 台"
	if req.GroupID > 0 && len(req.Targets) == 0 {
		targetDesc = "主机组"
	}
	payload := map[string]interface{}{}
	if len(req.Targets) > 0 {
		payload["targets"] = req.Targets
	}
	if req.GroupID > 0 {
		payload["group_id"] = req.GroupID
	}
	var title string
	if req.Playbook != "" {
		// playbook 分支：id 合法性 + 文件存在（executor 再按 path 执行）
		if !playbookIDRe.MatchString(req.Playbook) {
			Fail(c, http.StatusBadRequest, "playbook ID 非法")
			return
		}
		pbPath, _, ok := ansible.ResolvePlaybook(playbookDir(), req.Playbook)
		if !ok {
			Fail(c, http.StatusNotFound, "playbook 不存在")
			return
		}
		payload["playbook"] = req.Playbook
		// survey 参数处理：强校验答案 + secret 加密分流。
		//   - 非 secret → payload.extra_vars（executor 落盘为 --extra-vars @file）
		//   - secret    → 经 secretbox 加密后进 payload.secret_vars，明文不落库、不进命令行，
		//     执行时解密走环境变量（见 ansible.ApplySurveyAnswers）
		var surveyVars []ansible.SurveyVar
		if raw, rerr := os.ReadFile(pbPath); rerr == nil {
			if sv, present, perr := ansible.ParseSurvey(raw); perr == nil && present {
				surveyVars = sv
			}
		}
		answers := map[string]string{}
		for k, v := range req.ExtraVars {
			answers[k] = fmt.Sprintf("%v", v)
		}
		if len(surveyVars) > 0 {
			if verr := ansible.ValidateAnswers(surveyVars, answers); verr != nil {
				Fail(c, http.StatusBadRequest, verr.Error())
				return
			}
		}
		byName := ansible.SurveyByName(surveyVars)
		extraOut := map[string]string{}
		secretOut := map[string]map[string]string{}
		for name, val := range answers {
			if v, declared := byName[name]; declared && v.Type == ansible.SurveyTypeSecret {
				if h.MasterSecret == "" {
					Fail(c, http.StatusServiceUnavailable, "凭据加密未初始化，无法安全保存 secret 参数")
					return
				}
				enc, salt, eerr := secretbox.SealWithMaster(h.MasterSecret, val)
				if eerr != nil {
					ErrorWithMessage(c, http.StatusInternalServerError, "加密执行参数失败", eerr)
					return
				}
				secretOut[name] = map[string]string{"enc": enc, "salt": salt}
				continue
			}
			extraOut[name] = val
		}
		if len(extraOut) > 0 {
			payload["extra_vars"] = extraOut
		}
		if len(secretOut) > 0 {
			payload["secret_vars"] = secretOut
		}
		if req.Check {
			payload["check"] = true
		}
		if req.Diff {
			payload["diff"] = true
		}
		if strings.TrimSpace(req.Tags) != "" {
			payload["tags"] = strings.TrimSpace(req.Tags)
		}
		if strings.TrimSpace(req.SkipTags) != "" {
			payload["skip_tags"] = strings.TrimSpace(req.SkipTags)
		}
		title = "Ansible playbook " + req.Playbook + " → " + targetDesc
	} else {
		req.Module = strings.TrimSpace(req.Module)
		if !ansibleModules[req.Module] {
			Fail(c, http.StatusBadRequest, "模块仅支持 "+ansibleModuleList)
			return
		}
		if req.Module != "ping" && strings.TrimSpace(req.Args) == "" {
			Fail(c, http.StatusBadRequest, "模块 "+req.Module+" 需要填写执行参数")
			return
		}
		payload["module"] = req.Module
		payload["args"] = req.Args
		title = "Ansible " + req.Module + " → " + targetDesc
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("ansible_run", title, payload, userID, username, "", nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	// 留痕：谁、从哪、对几台机器跑了什么（shell 参数/playbook 内容不进日志——可能含敏感内容）
	log.Printf("[ansible] 提交批量执行 playbook=%q module=%q targets=%d group=%d user=%s from=%s",
		req.Playbook, req.Module, len(req.Targets), req.GroupID, username, c.ClientIP())
	Accepted(c, "批量执行任务已提交", gin.H{"task_id": task.ID})
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

// ---------- 共享 role 库（data/ansible/roles/）----------
//
// role 与 playbook 的区别：playbook 是「一次执行」，role 是「可复用的能力单元」。
// 共享库让 role 脱离单个 playbook 目录独立存在，多个 playbook 引用同一份实现；
// 想特化的 playbook 在本地放同名 role 即可覆盖（优先级已实测）。

// roleItem 清单条目：RoleMeta + 是否内置种子（种子判定依赖仓库路径，故在 handler 侧补）。
type roleItem struct {
	ansible.RoleMeta
	BuiltIn bool `json:"built_in"`
}

// ListRoles GET /api/ansible/roles
func (h *AnsibleHandler) ListRoles(c *gin.Context) {
	items, err := ansible.ListRoles(roleDir())
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取 role 库失败", err)
		return
	}
	out := make([]roleItem, 0, len(items))
	for _, m := range items {
		_, serr := os.Stat(filepath.Join(seedRoleDir(), m.Name))
		out = append(out, roleItem{RoleMeta: m, BuiltIn: serr == nil})
	}
	Success(c, gin.H{"items": out, "total": len(out)})
}

// CreateRole POST /api/ansible/roles body {name*, desc?} —— 按标准骨架新建。
func (h *AnsibleHandler) CreateRole(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数格式非法")
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := ansible.ScaffoldRole(roleDir(), name, strings.TrimSpace(req.Desc)); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[ansible] 新建共享 role %s by=%s from=%s", name, c.GetString("username"), c.ClientIP())
	Success(c, gin.H{"name": name, "created": true})
}

// DeleteRole DELETE /api/ansible/roles/:name —— 整目录删除。
// 不检查「是否被 playbook 引用」：引用关系藏在 playbook 正文里，静态扫不干净；
// 而删掉后的表现是执行时明确的「role 不存在」报错，比留一堆猜疑更清楚。
func (h *AnsibleHandler) DeleteRole(c *gin.Context) {
	name := c.Param("name")
	if err := ansible.RemoveDir(roleDir(), name); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[ansible] 删除共享 role %s by=%s from=%s", name, c.GetString("username"), c.ClientIP())
	Success(c, gin.H{"name": name, "deleted": true})
}

// ListRoleFiles GET /api/ansible/roles/:name/files
func (h *AnsibleHandler) ListRoleFiles(c *gin.Context) {
	files, err := ansible.ListDirFiles(roleDir(), c.Param("name"))
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"items": files})
}

// ReadRoleFile GET /api/ansible/roles/:name/file?path=tasks/main.yml
func (h *AnsibleHandler) ReadRoleFile(c *gin.Context) {
	content, err := ansible.ReadDirFile(roleDir(), c.Param("name"), c.Query("path"))
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"content": content})
}

// WriteRoleFile PUT /api/ansible/roles/:name/file body {path, content}
// 写后做一次 role 语法校验（把 role 临时挂进一个最小 playbook 跑 --syntax-check）：
// role 单独没有「语法检查」入口，但它最终是被 playbook 加载的，等价校验最接近真实用法。
func (h *AnsibleHandler) WriteRoleFile(c *gin.Context) {
	name := c.Param("name")
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		Fail(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	if err := ansible.WriteDirFile(roleDir(), name, req.Path, req.Content); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	warning := ""
	if err := ansible.SyntaxCheckRole(roleDir(), name); err != nil {
		warning = err.Error()
	}
	Success(c, gin.H{"saved": true, "warning": warning})
}

// UploadRoleFile POST /api/ansible/roles/:name/upload（multipart：file + path=相对路径）
func (h *AnsibleHandler) UploadRoleFile(c *gin.Context) {
	name := c.Param("name")
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
	n, err := ansible.SaveDirUpload(roleDir(), name, rel, src)
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已上传", "size": n})
}

// DeleteRoleFile DELETE /api/ansible/roles/:name/file?path=
func (h *AnsibleHandler) DeleteRoleFile(c *gin.Context) {
	if err := ansible.DeleteDirFile(roleDir(), c.Param("name"), c.Query("path")); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已删除"})
}
