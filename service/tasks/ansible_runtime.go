// ansible_runtime.go：ansible_run 的运行期辅助——run 级 ansible.cfg、inventory 脱敏与 PLAY RECAP 解析。
package tasks

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/service/ansible"
)

// sanitizeInventory 读取本次生成的 inventory 并脱敏（口令替换为 ***），供前端展示——
// 「inventory 长什么样」是理解 ansible 的第一步，此前只存在于跑完即删的瞬时文件里，完全看不见。
func sanitizeInventory(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	for i, ln := range lines {
		if idx := strings.Index(ln, "ansible_ssh_pass="); idx >= 0 {
			lines[i] = ln[:idx+len("ansible_ssh_pass=")] + "***（已脱敏）"
		}
	}
	return strings.Join(lines, "\n")
}

// writeRunAnsibleConfig 生成 run 级 ansible.cfg 并返回路径（随 run 目录删除）。
// 补上平台此前完全缺失的 ansible 配置：forks、facts 缓存（jsonfile，持久目录）、
// interpreter_python（免 sudo/python 缺失告警）、pipelining、共享 role 库搜索路径。
// fact 缓存目录跨 run 保留。
func writeRunAnsibleConfig(runDir string) string {
	// 路径必须绝对化：ansible 子进程按「自己进程的 CWD」解析 cfg 里的相对路径，
	// 而 DATA_DIR 默认就是相对的 "data"——相对路径会变成 <run 目录>/data/ansible/roles，
	// 共享 role 与 fact 缓存全部指向不存在的位置（实测踩过：role 报「not found in:
	// .../runs/200-xxx/data/ansible/roles」）。平台自身的 cwd 在启动时固定，所以
	// 在服务进程内相对路径看着是对的，一旦交给子进程就崩——交接点必须绝对化。
	cacheDir := absDataPath("ansible", "factcache")
	_ = os.MkdirAll(cacheDir, 0o700)
	// 共享 role 库：让 data/ansible/roles/ 下的 role 对全部 playbook 可见（不必每个
	// playbook 复制一份）。优先级已实测：playbook 本地 roles/<同名> 覆盖共享库同名。
	// 路径取 ansible.SharedRolesDir（装配层注入），保证与保存前 --syntax-check 用的一致。
	rolesDir := ansible.SharedRolesDir
	if rolesDir == "" {
		rolesDir = absDataPath("ansible", "roles")
	} else if abs, aerr := filepath.Abs(rolesDir); aerr == nil {
		rolesDir = abs
	}
	_ = os.MkdirAll(rolesDir, 0o755)
	content := "# 由 vmops 生成（run 级，随 run 目录删除）\n" +
		"[defaults]\n" +
		"host_key_checking = False\n" +
		"interpreter_python = auto_silent\n" +
		"deprecation_warnings = False\n" +
		"forks = 20\n" +
		"gathering = smart\n" +
		"fact_caching = jsonfile\n" +
		"fact_caching_connection = " + cacheDir + "\n" +
		"fact_caching_timeout = 86400\n" +
		"nocows = 1\n" +
		"roles_path = " + rolesDir + "\n" +
		"\n[ssh_connection]\n" +
		"pipelining = True\n"
	path := filepath.Join(runDir, "ansible.cfg")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "" // 写失败不阻断（退回 ansible 默认发现）
	}
	return path
}

// parseRecap 解析 PLAY RECAP 段（行形如 "host : ok=2 changed=1 unreachable=0 failed=0 ..."）。
func parseRecap(recapText string) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, line := range strings.Split(recapText, "\n") {
		idx := strings.Index(line, " : ")
		if idx <= 0 || !strings.Contains(line, "ok=") {
			continue
		}
		host := strings.TrimSpace(line[:idx])
		counts := map[string]int{}
		for _, kv := range strings.Fields(line[idx+3:]) {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				continue
			}
			n, err := strconv.Atoi(parts[1])
			if err == nil {
				counts[parts[0]] = n
			}
		}
		out[host] = counts
	}
	return out
}

// absDataPath 取数据目录下的绝对路径。写进交给子进程的配置（ansible.cfg）时必须用它：
// 子进程按自己的 CWD 解析相对路径，而 DATA_DIR 默认是相对的 "data"。
// 解析失败退回原值（服务进程内相对路径仍可用，至少不比改前更差）。
func absDataPath(parts ...string) string {
	p := config.DataPath(parts...)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
