// role_fs.go：Ansible「共享 role 库」——让一个 role 能被多个 playbook 复用。
//
// 此前 role 只能长在某个 playbook 目录里（<playbook>/roles/<role>/）：换一个 playbook
// 想用同一个 role，只能复制一份，改一处要记得同步另一处。本文件把 role 提升为一等资产：
// data/ansible/roles/<role>/，执行时经生成的 ansible.cfg 里的 roles_path 对全部 playbook 可见。
//
// 优先级（已实测）：playbook 本地 roles/<同名> 覆盖共享库里的同名 role——
// 共享库给默认实现，个别 playbook 要特化就在本地放一份覆盖它。
package ansible

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RoleMeta 共享 role 的清单条目。
type RoleMeta struct {
	Name      string `json:"name"`
	Desc      string `json:"desc"`
	FileCount int    `json:"file_count"`
	UpdatedAt string `json:"updated_at"`
}

// 标准 role 骨架：文件路径 → 初始内容（{{name}} / {{desc}} 占位）。
// templates/ 与 files/ 是空目录，首次上传/新建文件时才出现内容。
var roleScaffold = map[string]string{
	"tasks/main.yml": "# vmops-role: name={{name}} | desc={{desc}}\n" +
		"---\n" +
		"# role 任务入口：被 playbook 以 `roles: [{{name}}]` 引用。\n" +
		"# 幂等是硬要求——同一个 role 会被反复 apply，任务必须可重入。\n" +
		"- name: {{desc}}\n" +
		"  ansible.builtin.debug:\n" +
		"    msg: \"role {{name}} 已应用（改这里换成真实任务）\"\n",
	"handlers/main.yml": "---\n" +
		"# handler：仅在被 notify 时执行，且一轮 play 内只跑一次。\n" +
		"# - name: restart {{name}}\n" +
		"#   ansible.builtin.service:\n" +
		"#     name: {{name}}\n" +
		"#     state: restarted\n",
	"defaults/main.yml": "---\n" +
		"# 默认变量：优先级最低，调用方可用 playbook 的 vars / -e 覆盖。\n" +
		"# {{name}}_enabled: true\n",
	"vars/main.yml": "---\n" +
		"# 角色内部变量：优先级高，调用方覆盖不了（该给调用方调的放 defaults/）。\n",
	"meta/main.yml": "---\n" +
		"galaxy_info:\n" +
		"  role_name: {{name}}\n" +
		"  description: {{desc}}\n" +
		"  license: MIT\n" +
		"  min_ansible_version: \"2.9\"\n" +
		"dependencies: []\n",
	"README.md": "# {{name}}\n\n{{desc}}\n\n" +
		"用法（任何 playbook 都可引用，无需复制）：\n\n" +
		"```yaml\n- hosts: all\n  roles:\n    - {{name}}\n```\n\n" +
		"变量见 `defaults/main.yml`。要改这个 role 请用平台「运维自动化 → Role 库」，\n" +
		"改完所有引用它的 playbook 同时生效。\n",
}

// ScaffoldRole 新建共享 role（标准目录骨架 + 一份可读的 README）。
func ScaffoldRole(baseDir, name, desc string) error {
	if !dirNameRe.MatchString(name) {
		return errors.New("role 名非法（1-64 位，字母或数字开头，仅允许字母数字与 _ . -）")
	}
	if strings.TrimSpace(desc) == "" {
		desc = "自定义 role"
	}
	root := filepath.Join(baseDir, name)
	if _, err := os.Stat(root); err == nil {
		return errors.New("同名 role 已存在")
	}
	// 先写内容再建空目录：中途失败时残留一个可读的 role，好过半个空壳
	for rel, tpl := range roleScaffold {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fmt.Errorf("创建 role 目录失败: %w", err)
		}
		content := strings.NewReplacer("{{name}}", name, "{{desc}}", desc).Replace(tpl)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", rel, err)
		}
	}
	for _, d := range []string{"templates", "files"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return fmt.Errorf("创建 %s 目录失败: %w", d, err)
		}
	}
	return nil
}

// ListRoles 列共享 role 库（目录不存在时返回空）。
func ListRoles(baseDir string) ([]RoleMeta, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 role 库失败: %w", err)
	}
	out := make([]RoleMeta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !dirNameRe.MatchString(e.Name()) {
			continue
		}
		meta := RoleMeta{Name: e.Name(), Desc: e.Name()}
		if info, err := e.Info(); err == nil {
			meta.UpdatedAt = info.ModTime().Format("2006-01-02 15:04:05")
		}
		if files, err := ListDirFiles(baseDir, e.Name()); err == nil {
			meta.FileCount = len(files)
		}
		if desc := roleDescription(baseDir, e.Name()); desc != "" {
			meta.Desc = desc
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// roleDescription 从 tasks/main.yml 头部读 `# vmops-role: name=x | desc=y`（与 playbook
// 的 `# vmops-playbook:` 约定同形，只看前 8 行）；没有就回落 meta/main.yml 的
// galaxy_info.description；再没有返回空串（调用方用 role 名兜底）。
func roleDescription(baseDir, name string) string {
	if raw, err := os.ReadFile(filepath.Join(baseDir, name, "tasks", "main.yml")); err == nil {
		for i, line := range strings.Split(string(raw), "\n") {
			if i > 7 {
				break
			}
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "# vmops-role:") {
				continue
			}
			for _, part := range strings.Split(strings.TrimPrefix(line, "# vmops-role:"), "|") {
				kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
				if len(kv) == 2 && strings.TrimSpace(kv[0]) == "desc" {
					return strings.TrimSpace(kv[1])
				}
			}
			break
		}
	}
	if raw, err := os.ReadFile(filepath.Join(baseDir, name, "meta", "main.yml")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if i := strings.Index(line, "description:"); i >= 0 {
				if v := strings.TrimSpace(line[i+len("description:"):]); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// SyntaxCheckRole 校验一个共享 role 能否被 playbook 正常加载。
//
// role 本身没有独立的语法检查入口（ansible 不为 role 提供 --syntax-check），但它最终
// 一定是被 playbook 加载的——所以造一个只引用该 role 的最小 playbook，在临时目录里跑
// 等价校验。这样能抓到 YAML 写坏、handler 名重复、tasks 引用了不存在的变量文件等问题，
// 而不是等真正执行时才炸。
func SyntaxCheckRole(baseDir, name string) error {
	if !dirNameRe.MatchString(name) {
		return errors.New("role 名非法")
	}
	if st, err := os.Stat(filepath.Join(baseDir, name)); err != nil || !st.IsDir() {
		return errors.New("role 不存在")
	}
	eng, err := Detect()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "vmops-rolecheck-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmp)
	pb := "---\n- hosts: all\n  gather_facts: false\n  roles:\n    - " + name + "\n"
	target := filepath.Join(tmp, "playbook.yml")
	if err := os.WriteFile(target, []byte(pb), 0o600); err != nil {
		return fmt.Errorf("写入临时 playbook 失败: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// 库目录显式指到本 role 所在的库：校验不依赖装配层注入的全局值（测试里是临时目录）
	out, err := execCombinedEnv(ctx, []string{"ANSIBLE_ROLES_PATH=" + baseDir}, eng.PlaybookPath, "--syntax-check", "-i", "localhost,", target)
	if err == nil {
		return nil
	}
	out = strings.ReplaceAll(out, tmp, "<role-check>")
	msg := strings.TrimSpace(out)
	if msg == "" {
		msg = err.Error()
	}
	return errors.New(truncateStr("role 校验未通过: "+msg, 500))
}
