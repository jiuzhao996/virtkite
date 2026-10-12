// dirfs.go：ansible 内容目录（playbook 目录 / role 目录）共用的「受限文件树」操作。
//
// 两处用的是同一套语义——一个受管目录 + 一个名字白名单 + 相对路径词法守卫 +
// 1MB 文本读写上限——抽出来只为不写两遍（路径守卫是最不该复制粘贴的代码：
// 抄一份就多一个可能漏 `..` 变体的注入面）。
package ansible

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// dirNameRe 目录名白名单（playbook id / role 名共用）。作目录名，必须安全。
var dirNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// dirContentMaxBytes 文本读写上限：超过则拒（防误读大文件把内存打爆）。
const dirContentMaxBytes = 1 << 20

// DirFile 受管目录内的一个文件条目（相对路径统一用 / 分隔）。
type DirFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ValidDirName 目录名白名单校验（handler 侧先查一次，给出友好的 400 文案）。
func ValidDirName(name string) bool { return dirNameRe.MatchString(name) }

// safeDirPath 把相对路径解析到 <baseDir>/<name> 内（词法守卫：必须落在根内且带分隔符边界）。
// 唯一的路径注入面，所有文件操作都必须先过这里。
func safeDirPath(baseDir, name, rel string) (string, error) {
	if !dirNameRe.MatchString(name) {
		return "", errors.New("名称非法（1-64 位，字母或数字开头，仅允许字母数字与 _ . -）")
	}
	root := filepath.Join(baseDir, name)
	clean := filepath.Clean(filepath.Join(root, rel))
	if clean != root && !strings.HasPrefix(clean, root+string(os.PathSeparator)) {
		return "", errors.New("路径非法（越出目录）")
	}
	return clean, nil
}

// ListDirFiles 列受管目录内的文件（递归，只列文件，按路径字典序）。
func ListDirFiles(baseDir, name string) ([]DirFile, error) {
	root := filepath.Join(baseDir, name)
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return nil, errors.New("目录不存在")
	}
	var out []DirFile
	werr := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		out = append(out, DirFile{Path: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	if werr != nil {
		return nil, fmt.Errorf("遍历目录失败: %w", werr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ReadDirFile 读受管目录内的文本文件（限制 1MB）。
func ReadDirFile(baseDir, name, rel string) (string, error) {
	p, err := safeDirPath(baseDir, name, rel)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return "", errors.New("文件不存在")
	}
	if st.Size() > dirContentMaxBytes {
		return "", errors.New("文件过大（>1MB）")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	return string(b), nil
}

// WriteDirFile 写受管目录内的文本文件（自动建父目录）。
func WriteDirFile(baseDir, name, rel, content string) error {
	p, err := safeDirPath(baseDir, name, rel)
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

// SaveDirUpload 上传文件到受管目录内（二进制安全，用于 tar/证书等）。
func SaveDirUpload(baseDir, name, rel string, r io.Reader) (int64, error) {
	p, err := safeDirPath(baseDir, name, rel)
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

// DeleteDirFile 删除受管目录内的文件或子目录（根目录本身不可删）。
func DeleteDirFile(baseDir, name, rel string) error {
	p, err := safeDirPath(baseDir, name, rel)
	if err != nil {
		return err
	}
	if p == filepath.Join(baseDir, name) {
		return errors.New("不能删除根目录")
	}
	if _, err := os.Stat(p); err != nil {
		return errors.New("文件不存在")
	}
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("删除失败: %w", err)
	}
	return nil
}

// RemoveDir 删除整个受管目录（名字先过白名单，防 `..` 之类把 RemoveAll 指到别处）。
func RemoveDir(baseDir, name string) error {
	if !dirNameRe.MatchString(name) {
		return errors.New("名称非法")
	}
	dir := filepath.Join(baseDir, name)
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return errors.New("目录不存在")
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除目录失败: %w", err)
	}
	return nil
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
