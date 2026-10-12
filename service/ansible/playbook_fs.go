// playbook_fs.go：playbook 库的「目录形态」支持——让 playbook 能以目录承载 role。
//
// 此前平台只认顶层 *.yml 单文件：保存时的 --syntax-check 把内容复制成临时单文件再校验，
// 于是 `roles:` 引用会在保存阶段就报「角色目录不存在」——笔记 Ansible05 整篇（roles/galaxy
// 目录结构）在平台里根本落不了地。本文件补上：
//   - 一个 playbook 可以是 <id>.yml（单文件）或 <id>/（目录，主文件 site.yml 或首个 *.yml）
//   - 目录形态下 roles/ templates/ files/ handlers/ 等子目录随主文件一起被 ansible 识别
//   - 目录内文件可经 API 增删改（复用同一套 safePath 守卫）
package ansible

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// pbNameRe playbook id / 目录名白名单（与 handler 侧 playbookIDRe 同规）。
var pbNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// ResolvePlaybook 解析 playbook id 到主文件路径。
// 优先 <base>/<id>.yml（单文件），其次 <base>/<id>/site.yml，再退该目录下首个 *.yml。
// 返回 (主文件路径, 是否目录形态, 是否找到)。
func ResolvePlaybook(baseDir, id string) (string, bool, bool) {
	if !pbNameRe.MatchString(id) {
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
	if !pbNameRe.MatchString(id) {
		return false
	}
	st, err := os.Stat(filepath.Join(baseDir, id))
	return err == nil && st.IsDir()
}

// PlaybookFile 目录内文件条目。
type PlaybookFile struct {
	Path string `json:"path"` // 相对 playbook 目录
	Size int64  `json:"size"`
}

// safePlaybookPath 把相对路径解析到 playbook 目录内（词法守卫，禁止逃出）。
func safePlaybookPath(baseDir, id, rel string) (string, error) {
	if !pbNameRe.MatchString(id) {
		return "", errors.New("playbook ID 非法")
	}
	root := filepath.Join(baseDir, id)
	clean := filepath.Clean(filepath.Join(root, rel))
	if clean != root && !strings.HasPrefix(clean, root+string(os.PathSeparator)) {
		return "", errors.New("路径非法（越出 playbook 目录）")
	}
	return clean, nil
}

// ListPlaybookFiles 列目录形态 playbook 的文件（递归，只列文件）。
func ListPlaybookFiles(baseDir, id string) ([]PlaybookFile, error) {
	root := filepath.Join(baseDir, id)
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return nil, errors.New("该 playbook 不是目录形态")
	}
	var out []PlaybookFile
	werr := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		out = append(out, PlaybookFile{Path: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	if werr != nil {
		return nil, fmt.Errorf("遍历 playbook 目录失败: %w", werr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ReadPlaybookFile 读目录内文本文件（限制 1MB）。
func ReadPlaybookFile(baseDir, id, rel string) (string, error) {
	p, err := safePlaybookPath(baseDir, id, rel)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return "", errors.New("文件不存在")
	}
	if st.Size() > 1<<20 {
		return "", errors.New("文件过大（>1MB）")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	return string(b), nil
}

// WritePlaybookFile 写目录内文本文件（自动建父目录）。
func WritePlaybookFile(baseDir, id, rel, content string) error {
	p, err := safePlaybookPath(baseDir, id, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}
	return nil
}

// SavePlaybookUpload 上传文件到目录内（二进制安全）。
func SavePlaybookUpload(baseDir, id, rel string, r io.Reader) (int64, error) {
	p, err := safePlaybookPath(baseDir, id, rel)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, fmt.Errorf("创建目录失败: %w", err)
	}
	f, err := os.Create(p)
	if err != nil {
		return 0, fmt.Errorf("创建文件失败: %w", err)
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	if err != nil {
		return 0, fmt.Errorf("写入文件失败: %w", err)
	}
	return n, nil
}

// DeletePlaybookFile 删除目录内文件或子目录。
func DeletePlaybookFile(baseDir, id, rel string) error {
	p, err := safePlaybookPath(baseDir, id, rel)
	if err != nil {
		return err
	}
	if p == filepath.Join(baseDir, id) {
		return errors.New("不能删除 playbook 根目录")
	}
	if _, err := os.Stat(p); err != nil {
		return errors.New("文件不存在")
	}
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("删除失败: %w", err)
	}
	return nil
}

// SyntaxCheckDir 目录形态 playbook 的语法校验：整目录复制到临时目录再 --syntax-check
// （roles/templates 等相对引用必须同目录可见，否则误报「角色不存在」）。
func SyntaxCheckDir(mainFile string) error {
	raw, err := os.ReadFile(mainFile)
	if err != nil {
		return fmt.Errorf("读取主文件失败: %w", err)
	}
	return SyntaxCheckContentInDir(mainFile, string(raw))
}

// SyntaxCheckContentInDir 用给定内容覆盖主文件后做整目录语法校验（保存前预检用）：
// 复制整目录到临时目录，写入新内容到主文件位置，再 --syntax-check——保证 roles/
// 相对引用可见，避免「编辑 site.yml 时误报角色不存在」。
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

// copyTree 递归复制目录（文件权限 0600，目录 0755）。
func copyTree(src, dst string) error {
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
		return os.WriteFile(target, b, 0o600)
	})
}
