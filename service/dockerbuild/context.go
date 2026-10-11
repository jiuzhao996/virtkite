// Package dockerbuild 管理 docker build 的「构建上下文」：一个上下文 = data/docker-builds/<name>/
// 下的一个目录，内含 Dockerfile 与需要 COPY 进镜像的文件。
//
// 平台此前只能拉镜像不能造镜像；本包补上 Dockerfile 构建这条教学主线（笔记 Docker04/05/07/09）。
// 所有路径操作都过 safePath 守卫（词法前缀 + 分隔符边界），禁止逃出上下文目录。
package dockerbuild

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jiuzhao/vmops/config"
)

// nameRe 上下文名白名单（作目录名，须安全）。
var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// BaseDir 构建上下文根目录（随 DATA_DIR）。
func BaseDir() string { return config.DataPath("docker-builds") }

// ContextInfo 一个构建上下文的概要。
type ContextInfo struct {
	Name      string `json:"name"`
	FileCount int    `json:"file_count"`
	HasDocker bool   `json:"has_dockerfile"` // 根下有 Dockerfile
	UpdatedAt int64  `json:"updated_at"`     // 目录 mtime（unix 秒）
}

// FileInfo 上下文内的一个文件。
type FileInfo struct {
	Path string `json:"path"` // 相对上下文根
	Size int64  `json:"size"`
}

// ValidateName 校验上下文名。
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return errors.New("名称非法（1-64 位，字母或数字开头，仅允许字母数字与 _ . -）")
	}
	return nil
}

// safePath 把相对路径解析到上下文目录内（词法守卫：必须落在根内且带分隔符边界）。
func safePath(name, rel string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	root := filepath.Join(BaseDir(), name)
	clean := filepath.Clean(filepath.Join(root, rel))
	if clean != root && !strings.HasPrefix(clean, root+string(os.PathSeparator)) {
		return "", errors.New("路径非法（越出构建上下文）")
	}
	return clean, nil
}

// List 列出全部构建上下文（目录不存在时返回空）。
func List() ([]ContextInfo, error) {
	entries, err := os.ReadDir(BaseDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取构建目录失败: %w", err)
	}
	out := make([]ContextInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := ContextInfo{Name: e.Name()}
		if fi, err := e.Info(); err == nil {
			info.UpdatedAt = fi.ModTime().Unix()
		}
		if files, err := ListFiles(e.Name(), "."); err == nil {
			info.FileCount = len(files)
			for _, f := range files {
				if f.Path == "Dockerfile" {
					info.HasDocker = true
				}
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Create 新建构建上下文目录。
func Create(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	dir, _ := safePath(name, ".")
	if _, err := os.Stat(dir); err == nil {
		return errors.New("同名构建上下文已存在")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建构建上下文失败: %w", err)
	}
	return nil
}

// Remove 删除构建上下文（整目录）。
func Remove(name string) error {
	dir, err := safePath(name, ".")
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		return errors.New("构建上下文不存在")
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除构建上下文失败: %w", err)
	}
	return nil
}

// ContextDir 返回上下文目录的绝对路径（存在性校验）。
func ContextDir(name string) (string, error) {
	dir, err := safePath(name, ".")
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", errors.New("构建上下文不存在")
	}
	return dir, nil
}

// ListFiles 列出子目录下的文件（rel="." 为根；只列一层，不递归）。
func ListFiles(name, rel string) ([]FileInfo, error) {
	dir, err := safePath(name, rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}
	root := filepath.Join(BaseDir(), name)
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		abs := filepath.Join(dir, e.Name())
		fi := FileInfo{Path: relTo(root, abs)}
		if info, err := e.Info(); err == nil {
			fi.Size = info.Size()
		}
		if e.IsDir() {
			fi.Path += "/"
		}
		out = append(out, fi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// relTo 计算相对上下文根的路径（分隔符统一为 /）。
func relTo(root, abs string) string {
	r, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

// ReadFile 读上下文内文本文件（限制 1MB，防误读大文件把内存打爆）。
func ReadFile(name, rel string) (string, error) {
	p, err := safePath(name, rel)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return "", errors.New("文件不存在")
	}
	if st.Size() > 1<<20 {
		return "", errors.New("文件过大（>1MB），请用上传方式或直接编辑磁盘文件")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	return string(b), nil
}

// WriteFile 写上下文内文本文件（自动建父目录）。
func WriteFile(name, rel, content string) error {
	p, err := safePath(name, rel)
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

// SaveUpload 把上传流落到上下文内（二进制安全，用于 jar/压缩包等）。
func SaveUpload(name, rel string, r io.Reader) (int64, error) {
	p, err := safePath(name, rel)
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

// DeleteFile 删除上下文内文件或目录。
func DeleteFile(name, rel string) error {
	p, err := safePath(name, rel)
	if err != nil {
		return err
	}
	if p == filepath.Join(BaseDir(), name) {
		return errors.New("不能删除上下文根目录")
	}
	if _, err := os.Stat(p); err != nil {
		return errors.New("文件不存在")
	}
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("删除失败: %w", err)
	}
	return nil
}
