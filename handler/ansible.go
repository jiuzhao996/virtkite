// ansible.go：运维自动化（P4 S1）——处理器装配与共享目录/种子约定。
// 目标只收 vm_id（服务端查库取 IP/凭据，白名单结构性成立）。playbook 库见
// ansible_playbooks.go，共享 role 库见 ansible_roles.go，批量执行见 ansible_run.go，
// 引擎状态与公钥分发见 ansible_key.go。
package handler

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/service/ansible"
	"github.com/jiuzhao/vmops/service/tasks"
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
