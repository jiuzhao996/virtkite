// playbook_fs.go：playbook 库的「目录形态」支持——让 playbook 能以目录承载 role。
//
// 此前平台只认顶层 *.yml 单文件：保存时的 --syntax-check 把内容复制成临时单文件再校验，
// 于是 `roles:` 引用会在保存阶段就报「角色目录不存在」——笔记 Ansible05 整篇（roles/galaxy
// 目录结构）在平台里根本落不了地。本文件补上：
//   - 一个 playbook 可以是 <id>.yml（单文件）或 <id>/（目录，主文件 site.yml 或首个 *.yml）
//   - 目录形态下 roles/ templates/ files/ handlers/ 等子目录随主文件一起被 ansible 识别
//   - 目录内文件可经 API 增删改（走 dirfs.go 的共用守卫）
//
// 目录内文件的通用读写（列/读/写/上传/删）在 dirfs.go，role 目录复用同一套。
package ansible

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ResolvePlaybook 解析 playbook id 到主文件路径。
// 优先 <base>/<id>.yml（单文件），其次 <base>/<id>/site.yml，再退该目录下首个 *.yml。
// 返回 (主文件路径, 是否目录形态, 是否找到)。
func ResolvePlaybook(baseDir, id string) (string, bool, bool) {
	if !dirNameRe.MatchString(id) {
		return "", false, false
	}
	single := filepath.Join(baseDir, id+".yml")
	if st, err := os.Stat(single); err == nil && !st.IsDir() {
		return single, false, true
	}
	dir := filepath.Join(baseDir, id)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", false, false
	}
	site := filepath.Join(dir, "site.yml")
	if st, err := os.Stat(site); err == nil && !st.IsDir() {
		return site, true, true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", true, false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".yml") {
			return filepath.Join(dir, e.Name()), true, true
		}
	}
	return "", true, false
}

// IsDirPlaybook 该 id 是否目录形态（目录存在）。
func IsDirPlaybook(baseDir, id string) bool {
	if !dirNameRe.MatchString(id) {
		return false
	}
	st, err := os.Stat(filepath.Join(baseDir, id))
	return err == nil && st.IsDir()
}

// SyntaxCheckContentInDir 用给定内容覆盖主文件后做整目录语法校验（保存前预检用）。
func SyntaxCheckContentInDir(mainFile, newContent string) error {
	return SyntaxCheckFileInDir(mainFile, mainFile, newContent)
}

// SyntaxCheckFileInDir 目录形态 playbook 的「指定文件」校验：以 anchor 定位 playbook 目录
// （整目录复制到临时目录保证 roles/ 相对引用可见），把 newContent 写进 target 对应位置再
// --syntax-check。编辑的若不是主文件（如目录下另一个入口 yml），校验的必须是被编辑的那个
// 文件——否则写坏一个副入口 playbook 会静默通过，跑起来才炸。
func SyntaxCheckFileInDir(anchor, target, newContent string) error {
	eng, err := Detect()
	if err != nil {
		return err
	}
	srcDir := filepath.Dir(anchor)
	rel, rerr := filepath.Rel(srcDir, target)
	if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("校验目标越出 playbook 目录")
	}
	tmp, err := os.MkdirTemp("", "vmops-pbdir-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(srcDir, tmp); err != nil {
		return err
	}
	dst := filepath.Join(tmp, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	if err := os.WriteFile(dst, []byte(newContent), 0o600); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := execCombined(ctx, eng.PlaybookPath, "--syntax-check", "-i", "localhost,", dst)
	if err == nil {
		return nil
	}
	// ansible 报错里会带上我们复制到的临时目录（/tmp/vmops-pbdir-xxx/），对用户是噪音：
	// 换成 playbook 目录标记，报错位置仍可读（如 roles:/tmp/xxx/roles → roles:<playbook>/roles）
	out = strings.ReplaceAll(out, tmp, "<playbook>")
	msg := strings.TrimSpace(out)
	if msg == "" {
		msg = err.Error()
	}
	if line := syntaxErrRe.FindStringSubmatch(out); line != nil {
		return fmt.Errorf("语法错误（第 %s 行附近）: %s", line[1], truncateStr(msg, 500))
	}
	return errors.New(truncateStr("语法校验未通过: "+msg, 500))
}
