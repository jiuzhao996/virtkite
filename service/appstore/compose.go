// compose.go 承载应用商店 v2 的安装/卸载/状态查询（docker compose 命令编排）。
// 包文档与目录协议见 appstore.go。
//
// 项目名固定用 `docker compose -p <key>` 指定（优先级高于 compose 文件 name: 与
// COMPOSE_PROJECT_NAME），因此应用包 compose 文件里不写 name:，卸载/状态查询用同一
// 项目名即可对上；--env-file .env 提供表单变量，compose 原生插值完成 ${VAR} 替换。
package appstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	composeFileName = "docker-compose.yml"
	envFileName     = ".env"

	installTimeout   = 10 * time.Minute // 首次安装含镜像拉取，给足 10 分钟
	uninstallTimeout = 5 * time.Minute
	statusTimeout    = 30 * time.Second

	// stderrTailLen ComposeError 里携带的 stderr 摘要长度（拉镜像的进度刷屏只保留尾部）
	stderrTailLen = 600
)

var (
	// catalogDir 内置应用包目录（相对进程工作目录；单机同目录部署形态）
	catalogDir = "conf/appstore"
	// appsDataDir 应用安装目录：data/apps/<key>/，即 compose 工作目录，
	// compose 文件里的 ./data 相对挂载都落在这里
	appsDataDir = "data/apps"

	// composeExec docker compose 执行入口（var 注入点：单测替换成桩，不真跑 docker）
	composeExec = composeExecReal
)

// ComposeStatus 已装应用的运行概览
type ComposeStatus struct {
	Name     string `json:"name"`     // 应用 key（即 compose 项目名）
	Services int    `json:"services"` // compose ps 返回的服务实例数
	Running  int    `json:"running"`  // 其中处于 running 状态的容器数
	AppDir   string `json:"app_dir"`  // 安装目录
}

// ComposeError docker compose 命令失败。Stderr 摘要随错误携带——安装失败的原因
// （镜像拉不动、端口被占、yaml 语法错）几乎都在 compose/docker 输出里，属于必须透出给
// 操作者的运维信息，与 terminal WS 帧同口径的显式例外（完整错误同时进 handler 日志）。
type ComposeError struct {
	Command string // 失败的完整命令（不含宿主机路径以外的敏感信息）
	Stderr  string // 尾部截断的 stderr 摘要
	Err     error  // 底层 exec 错误（超时/退出码）
}

func (e *ComposeError) Error() string {
	if strings.TrimSpace(e.Stderr) != "" {
		return fmt.Sprintf("docker compose 执行失败: %s", e.Stderr)
	}
	return fmt.Sprintf("docker compose 执行失败: %v", e.Err)
}

func (e *ComposeError) Unwrap() error { return e.Err }

// Install 安装应用：
//  1. 读包并校验 key/版本；
//  2. 参数校验（ValidateFields）+ 补默认值；
//  3. 拷贝应用包文件到 data/apps/<key>/（已存在则覆盖 yml/compose，保留 data/ 运行数据）；
//  4. 写 .env（每个表单项一行 KEY=VALUE）；
//  5. `docker compose -p <key> -f docker-compose.yml --env-file .env up -d`（10 分钟超时）。
//
// 校验在拷贝之前：参数不合法时不会留下半装目录。compose up 失败时安装目录保留
// （含 .env 与 compose 文件），便于排查与重装覆盖。
func Install(ctx context.Context, key, version string, values map[string]string) (string, error) {
	meta, _, srcDir, err := load(key, version)
	if err != nil {
		return "", err
	}
	merged := applyDefaults(meta.FormFields, values)
	if err := ValidateFields(meta.FormFields, merged); err != nil {
		return "", err
	}

	appDir := filepath.Join(appsDataDir, key)
	if err := copyAppPackage(srcDir, appDir); err != nil {
		return "", fmt.Errorf("拷贝应用包失败: %w", err)
	}
	if err := writeEnvFile(appDir, meta.FormFields, merged); err != nil {
		return "", fmt.Errorf("写入 %s 失败: %w", envFileName, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	if _, err := composeExec(runCtx, appDir, key, "up", "-d"); err != nil {
		return "", err
	}
	return appDir, nil
}

// Uninstall 卸载应用：`docker compose down`（removeData 时附 -v 连命名卷/匿名卷一起删）。
// removeData=true 同时删除安装目录（含 ./data 运行数据）；false 只 down、目录原样保留。
func Uninstall(key string, removeData bool) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	appDir := filepath.Join(appsDataDir, key)
	if _, err := os.Stat(filepath.Join(appDir, composeFileName)); err != nil {
		if os.IsNotExist(err) {
			// 从未安装：removeData 语义下顺手清理可能残留的目录，否则按未安装报错
			if removeData {
				if err := os.RemoveAll(appDir); err != nil {
					return fmt.Errorf("删除安装目录失败: %w", err)
				}
				return nil
			}
			return fmt.Errorf("%w: %s（未安装）", ErrNotFound, key)
		}
		return fmt.Errorf("检查安装目录失败: %w", err)
	}

	// .env 缺失时补空文件：compose 解析 compose 文件需要 --env-file 指向的文件存在
	envPath := filepath.Join(appDir, envFileName)
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		if err := os.WriteFile(envPath, nil, 0o600); err != nil {
			return fmt.Errorf("补写空 %s 失败: %w", envFileName, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), uninstallTimeout)
	defer cancel()
	args := []string{"down"}
	if removeData {
		args = append(args, "-v")
	}
	if _, err := composeExec(ctx, appDir, key, args...); err != nil {
		return err
	}
	if removeData {
		if err := os.RemoveAll(appDir); err != nil {
			return fmt.Errorf("删除安装目录失败: %w", err)
		}
	}
	return nil
}

// Status 返回全部已装应用（data/apps/ 下含 compose 文件的目录）的运行概览。
// 单个应用 `compose ps` 失败（如 docker daemon 未启动）不整体报错：该应用 Services/Running
// 记 0，靠上层自行判断；data/apps 不存在视为尚未装过任何应用，返回空数组。
func Status() ([]ComposeStatus, error) {
	entries, err := os.ReadDir(appsDataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ComposeStatus{}, nil
		}
		return nil, fmt.Errorf("读取应用安装目录失败: %w", err)
	}
	out := []ComposeStatus{}
	for _, e := range entries {
		if !e.IsDir() || !keyPattern.MatchString(e.Name()) {
			continue
		}
		appDir := filepath.Join(appsDataDir, e.Name())
		if _, err := os.Stat(filepath.Join(appDir, composeFileName)); err != nil {
			continue // 没有 compose 文件的目录不视为已装应用
		}
		st := ComposeStatus{Name: e.Name(), AppDir: appDir}
		ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
		stdout, err := composeExec(ctx, appDir, e.Name(), "ps", "--format", "json")
		cancel()
		if err == nil {
			st.Services, st.Running, _ = parseComposePs(stdout)
		}
		out = append(out, st)
	}
	return out, nil
}

// composeExecReal 执行 docker compose 子命令：cwd=安装目录、数组参数（无 shell 注入面）。
// 失败返回 *ComposeError（stderr 摘要截尾）。
func composeExecReal(ctx context.Context, dir, key string, extra ...string) ([]byte, error) {
	args := composeArgs(key, extra...)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, &ComposeError{
			Command: "docker " + strings.Join(args, " "),
			Stderr:  tailString(stderr.String(), stderrTailLen),
			Err:     err,
		}
	}
	return stdout.Bytes(), nil
}

// composeArgs docker compose 公共参数前缀：-p 指定项目名（与卸载/查询同一项目），
// -f/--env-file 用安装目录内的相对路径（调用方已设 cwd=安装目录）
func composeArgs(key string, extra ...string) []string {
	args := []string{"compose", "-p", key, "-f", composeFileName, "--env-file", envFileName}
	return append(args, extra...)
}

// copyAppPackage 把应用包 srcDir 下的**文件**（data.yml、docker-compose.yml）拷到 dstDir。
// 刻意跳过子目录：目标目录里已有的 data/（应用运行数据）在重装/升级时保持原样，绝不覆盖。
func copyAppPackage(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("读取应用包目录失败: %w", err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("创建安装目录失败: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return fmt.Errorf("读取包文件信息失败: %w", err)
		}
		if err := copyFile(
			filepath.Join(srcDir, e.Name()),
			filepath.Join(dstDir, e.Name()),
			info.Mode().Perm(),
		); err != nil {
			return err
		}
	}
	return nil
}

// copyFile 按源文件权限拷贝单个文件（目标已存在则覆盖内容）
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开源文件失败: %w", err)
	}
	defer func() {
		// 只读句柄的 Close 失败无补救动作，best-effort 忽略
		_ = in.Close()
	}()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("拷贝文件内容失败: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("落盘目标文件失败: %w", err)
	}
	return nil
}

// envFileContent 生成 .env 内容：按表单定义顺序每个 envKey 一行 KEY=VALUE。
// 可选项值为空也写出（KEY= 空值行），compose 按空字符串插值且不告警未定义变量。
func envFileContent(fields []FormField, values map[string]string) string {
	var b strings.Builder
	for _, f := range fields {
		b.WriteString(f.EnvKey)
		b.WriteString("=")
		b.WriteString(values[f.EnvKey])
		b.WriteString("\n")
	}
	return b.String()
}

// writeEnvFile 落盘 .env。0600：内容可能含数据库 root 密码等敏感值。
func writeEnvFile(dir string, fields []FormField, values map[string]string) error {
	for _, f := range fields {
		if !envKeyPattern.MatchString(f.EnvKey) {
			return fmt.Errorf("表单项 envKey %q 非法", f.EnvKey)
		}
	}
	path := filepath.Join(dir, envFileName)
	if err := os.WriteFile(path, []byte(envFileContent(fields, values)), 0o600); err != nil {
		return err
	}
	return nil
}

// parseComposePs 解析 `docker compose ps --format json` 输出，返回（服务实例数，running 数）。
// compose v2 各小版本格式不一：新版本输出 JSON 数组，旧版本输出 JSON Lines（每行一个对象），
// 两种都兼容；空输出返回 0,0。
func parseComposePs(out []byte) (services, running int, err error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return 0, 0, nil
	}
	var items []map[string]any
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return 0, 0, fmt.Errorf("解析 compose ps 输出失败: %w", err)
		}
	} else {
		for _, line := range bytes.Split(trimmed, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(line, &m); err != nil {
				return 0, 0, fmt.Errorf("解析 compose ps 输出失败: %w", err)
			}
			items = append(items, m)
		}
	}
	for _, m := range items {
		services++
		if state, _ := m["State"].(string); state == "running" {
			running++
		}
	}
	return services, running, nil
}

// tailString 取字符串尾部 n 字节（多字节字符可能截出残句，仅用于日志/错误摘要场景，可接受）
func tailString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
