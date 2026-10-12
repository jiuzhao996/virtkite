// 容器内文件浏览（借 1Panel 的 file-browser 能力，只读）。
//
// 为什么走 `docker exec <id> ls/cat` 而不是 Engine API 的 archive 接口：
// 本包一贯以 docker CLI 数组参数封装 docker 能力（零第三方依赖、天然继承宿主 docker 权限），
// 文件浏览也用同一路子即可；exec 的参数是数组传给 docker 的（无 shell），不存在命令注入。
//
// 容器里可能没有 ls/head（scratch、distroless 等精简镜像），此时 exec 会报
// "executable file not found"，这里统一转成中文友好文案，让前端优雅提示而不是抛原始错误。
package dockerx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// maxContainerFileBytes 单文件读取上限（与 dockerbuild.ReadFile 的 1MB 口径一致）：
// 文本查看器只服务配置/日志类小文件，超限直接拒绝，避免把内存打爆。
const maxContainerFileBytes = 1 << 20

// ContainerFile 容器内一个目录项（ls -la 单行解析结果）。
type ContainerFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`    // 权限串，如 -rw-r--r--
	IsDir  bool   `json:"is_dir"`  // 目录
	IsLink bool   `json:"is_link"` // 符号链接
	Link   string `json:"link,omitempty"`
}

// SafeContainerPath 容器内路径校验：必须是绝对路径（以 / 开头）、不含控制字符、长度受限。
// 绝对路径同时排除了「以 - 开头被 ls 当 flag」的风险（参数虽为数组，但 ls 仍会解析前导 -）。
func SafeContainerPath(p string) bool {
	if p == "" || len(p) > 4096 || !strings.HasPrefix(p, "/") {
		return false
	}
	for _, r := range p {
		if r == 0 || r == '\n' || r == '\r' {
			return false
		}
	}
	return true
}

// ListContainerDir 列出容器内某目录（等价 docker exec <id> ls -la -- <path>）。
// 容器未运行或容器内无 ls 时返回中文友好错误。
func (d *Dockerx) ListContainerDir(id, path string) ([]ContainerFile, error) {
	if !safeDockerName(id) {
		return nil, errors.New("容器 ID 非法")
	}
	if !SafeContainerPath(path) {
		return nil, errors.New("路径非法（须为容器内绝对路径）")
	}
	// 等价 docker exec <id> ls -la <path>（参数数组，无 shell 拼接；path 已校验为以 / 开头，
	// 不会被 ls 当 flag；不传 -- 是为了兼容 busybox ls）
	out, err := runTimeout(30*time.Second, "exec", id, "ls", "-la", path)
	if err != nil {
		return nil, containerExecError(err)
	}
	return parseContainerLS(out), nil
}

// ReadContainerFile 读取容器内文本文件（等价 docker exec <id> head -c <N> <path>）。
// 用 head -c 而非 cat：head 只输出前 N 字节，天然给内存封顶，不会把大文件整个读进来。
// 返回 (内容, 是否被截断, 错误)。二进制文件（含 NUL 字节）拒绝返回。
func (d *Dockerx) ReadContainerFile(id, path string) (string, bool, error) {
	if !safeDockerName(id) {
		return "", false, errors.New("容器 ID 非法")
	}
	if !SafeContainerPath(path) {
		return "", false, errors.New("路径非法（须为容器内绝对路径）")
	}
	limit := strconv.Itoa(maxContainerFileBytes + 1) // 多读 1 字节用于判定是否截断
	out, err := runTimeout(30*time.Second, "exec", id, "head", "-c", limit, path)
	if err != nil {
		return "", false, containerExecError(err)
	}
	truncated := false
	if len(out) > maxContainerFileBytes {
		out = out[:maxContainerFileBytes]
		truncated = true
	}
	if strings.IndexByte(out, 0) >= 0 {
		return "", false, errors.New("该文件疑似二进制（含空字节），无法以文本方式查看")
	}
	return out, truncated, nil
}

// containerExecError 把 docker exec 的原始报错转成中文友好文案：
//   - 容器未运行；
//   - 容器内缺 ls/head（scratch、distroless 等精简镜像）；
//   - 其余原样保留（作为内部细节进日志，handler 侧只回通用文案）。
func containerExecError(err error) error {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not running"):
		return errors.New("容器未运行，无法浏览文件")
	case strings.Contains(msg, "not found in $path") || strings.Contains(msg, "executable file not found"):
		return errors.New("容器内缺少 ls/head 命令（可能是 scratch/精简镜像），无法浏览文件")
	default:
		return fmt.Errorf("浏览容器文件失败: %w", err)
	}
}

// parseContainerLS 解析 `ls -la` 输出。核心思路：跳过 "total N" 与不合规行，
// 每行取前 8 个空白分隔的字段（权限/链接数/属主/属组/大小/月/日/时间），
// 其后到行尾的整段（trim）即为文件名——按「第 8 个字段结束位置」切分而非按空格 join，
// 保证文件名含空格时不被拆散；符号链接名再按 " -> " 拆出目标。
func parseContainerLS(out string) []ContainerFile {
	files := []ContainerFile{}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f, ok := parseLSLine(line)
		if !ok {
			continue
		}
		if f.Name == "." || f.Name == ".." {
			continue // 浏览器用面包屑导航，"." / ".." 是噪音
		}
		files = append(files, f)
	}
	return files
}

// parseLSLine 解析单行 ls -l 输出。找到第 8 个字段后的剩余文本作为名字。
func parseLSLine(line string) (ContainerFile, bool) {
	var f ContainerFile
	// 逐字段扫描，记录第 8 个字段结束后的下标
	idx := 0
	n := len(line)
	fields := make([]string, 0, 8)
	for len(fields) < 8 {
		for idx < n && (line[idx] == ' ' || line[idx] == '\t') {
			idx++
		}
		if idx >= n {
			return f, false // 字段不足，非 ls -l 数据行（如 "total 24"）
		}
		start := idx
		for idx < n && line[idx] != ' ' && line[idx] != '\t' {
			idx++
		}
		fields = append(fields, line[start:idx])
	}
	mode := fields[0]
	if len(mode) < 10 {
		return f, false // 权限串形如 -rw-r--r--（10 字符），短于此说明不是数据行
	}
	name := strings.TrimSpace(line[idx:])
	if name == "" {
		return f, false
	}
	f.Mode = mode
	f.IsDir = mode[0] == 'd'
	f.IsLink = mode[0] == 'l'
	if sz, err := strconv.ParseInt(fields[4], 10, 64); err == nil {
		f.Size = sz
	}
	if f.IsLink {
		if nameOnly, target, ok := strings.Cut(name, " -> "); ok {
			f.Name = nameOnly
			f.Link = target
			return f, true
		}
	}
	f.Name = name
	return f, true
}
