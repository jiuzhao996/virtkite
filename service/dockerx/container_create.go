// 容器创建（v3.2 规划 R8 收尾）：docker run -d 的参数构造与校验拆成纯函数，
// 不依赖 docker 守护进程即可单测（与 virt 层 buildCloneSpec 同一抽法）。
package dockerx

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ContainerOpts 创建容器的请求参数（POST /api/docker/containers 请求体）。
// 除 Name/Image 外均可为空；零值语义与 docker CLI 默认一致（不输出对应 flag）。
type ContainerOpts struct {
	Name    string   `json:"name"`    // 容器名（--name）
	Image   string   `json:"image"`   // 镜像引用（nginx:1.27 / repo/app@sha256:...）
	Ports   []string `json:"ports"`   // 端口映射（-p，host:container[/tcp|/udp] 或 ip:host:container[/proto]）
	Volumes []string `json:"volumes"` // 卷挂载（-v，src:dst[:ro|rw]）
	Envs    []string `json:"envs"`    // 环境变量（-e，KEY=VALUE，VALUE 可再含 =）
	Restart string   `json:"restart"` // 重启策略（--restart，空与 no 均为 docker 默认）
	Command string   `json:"command"` // 附加命令参数（跟在镜像名后，按空白拆分）
}

// containerNameRe 容器名白名单：1-64 位、字母数字开头，仅允许字母数字与 _. -
// （docker 命名约束的保守子集，首位禁 -_. 防被解析成 flag）。
var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// restartPolicies --restart 白名单（docker 语义）。
var restartPolicies = map[string]bool{
	"no":             true,
	"always":         true,
	"unless-stopped": true,
	"on-failure":     true,
}

// ValidateContainerOpts 校验创建参数（纯函数）。返回固定中文文案，可直接回显给前端。
// 空白列表项跳过——与 BuildRunArgs 的跳过规则一致，脏数据静默忽略而非报错。
func ValidateContainerOpts(opts ContainerOpts) error {
	if !containerNameRe.MatchString(opts.Name) {
		return errors.New("容器名非法（1-64 位，需以字母或数字开头，仅允许字母数字与 _ . -）")
	}
	if !safeImageRef(opts.Image) {
		return errors.New("镜像名非法（仅允许字母数字与 . : _ - / @，如 nginx:1.27）")
	}
	if opts.Restart != "" && !restartPolicies[opts.Restart] {
		return errors.New("restart 策略仅支持 no / always / unless-stopped / on-failure")
	}
	for _, p := range opts.Ports {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if err := validatePortMapping(p); err != nil {
			return err
		}
	}
	for _, v := range opts.Volumes {
		if strings.TrimSpace(v) == "" {
			continue
		}
		if err := validateVolumeMapping(normalizeVolumeMapping(v)); err != nil {
			return err
		}
	}
	for _, e := range opts.Envs {
		if strings.TrimSpace(e) == "" {
			continue
		}
		if err := validateEnvEntry(e); err != nil {
			return err
		}
	}
	return nil
}

// validatePortMapping 单条端口映射校验：host:container[/tcp|/udp] 或 ip:host:container[/proto]，
// 端口号须为 1-65535（等价 docker -p 的取值约束；IPv6 绑定与端口范围映射不在支持范围）。
func validatePortMapping(p string) error {
	spec := p
	if i := strings.LastIndex(spec, "/"); i >= 0 {
		proto := spec[i+1:]
		if proto != "tcp" && proto != "udp" {
			return fmt.Errorf("端口映射 %q 的协议仅支持 tcp/udp", p)
		}
		spec = spec[:i]
	}
	parts := strings.Split(spec, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return fmt.Errorf("端口映射 %q 格式应为 host:container 或 ip:host:container", p)
	}
	if len(parts) == 3 {
		if net.ParseIP(parts[0]) == nil {
			return fmt.Errorf("端口映射 %q 的绑定 IP 非法", p)
		}
		parts = parts[1:]
	}
	for _, port := range parts {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("端口映射 %q 的端口须为 1-65535 的数字", p)
		}
	}
	return nil
}

// normalizeVolumeMapping 卷挂载各段去首尾空白（如 " /data:/x" → "/data:/x"）。
// 校验与 BuildRunArgs 共用同一归一化：只在校验侧 trim 的话，校验能过而 docker 收到
// 带空白参数仍失败、handler 变 500。纯函数，可单测。
func normalizeVolumeMapping(v string) string {
	parts := strings.Split(v, ":")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ":")
}

// validateVolumeMapping 单条卷挂载校验：src:dst[:ro|rw]。dst 必须是容器内绝对路径；
// src 为宿主机绝对路径或命名卷名，此处只做非空约束（bind 与命名卷无法静态区分）。
// 入参应先经 normalizeVolumeMapping 归一化（调用方 ValidateContainerOpts 已做）。
func validateVolumeMapping(v string) error {
	parts := strings.Split(v, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return fmt.Errorf("卷挂载 %q 格式应为 src:dst[:ro|rw]", v)
	}
	if len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw" {
		return fmt.Errorf("卷挂载 %q 的读写模式仅支持 ro/rw", v)
	}
	if parts[0] == "" {
		return fmt.Errorf("卷挂载 %q 缺少源路径", v)
	}
	if !strings.HasPrefix(parts[1], "/") {
		return fmt.Errorf("卷挂载 %q 的容器内路径必须是绝对路径（以 / 开头）", v)
	}
	return nil
}

// validateEnvEntry 单条环境变量校验：KEY=VALUE（按第一个 = 切分，VALUE 可再含 =）。
func validateEnvEntry(e string) error {
	key, _, ok := strings.Cut(e, "=")
	if !ok || key == "" {
		return fmt.Errorf("环境变量 %q 缺少 KEY=VALUE 结构", e)
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return fmt.Errorf("环境变量 %q 的变量名含空白字符", e)
	}
	return nil
}

// BuildRunArgs 把创建参数映射为 docker run 参数（纯函数，不执行）：
// 等价 docker run -d --name X --restart R -p ... -v ... -e ... IMAGE [command...]。
// restart 为空或 no 时不输出 --restart（no 即 docker 默认）；空白列表项跳过；
// command 按空白拆分（strings.Fields 语义，多个连续空白视作一个分隔符），
// 故含空格的引号参数（如 -g 'daemon off;'）会被拆成多个词——前端契约即按此约定传参。
func BuildRunArgs(opts ContainerOpts) []string {
	args := []string{"run", "-d", "--name", opts.Name}
	if opts.Restart != "" && opts.Restart != "no" {
		args = append(args, "--restart", opts.Restart)
	}
	for _, p := range opts.Ports {
		if strings.TrimSpace(p) != "" {
			args = append(args, "-p", p)
		}
	}
	for _, v := range opts.Volumes {
		if strings.TrimSpace(v) != "" {
			// 与校验同一归一化：各段去首尾空白后再透传给 docker，
			// 防 " /data:/x" 这类输入校验能过、docker 却因参数带空白报错
			args = append(args, "-v", normalizeVolumeMapping(v))
		}
	}
	for _, e := range opts.Envs {
		if strings.TrimSpace(e) != "" {
			args = append(args, "-e", e)
		}
	}
	args = append(args, opts.Image)
	if fields := strings.Fields(opts.Command); len(fields) > 0 {
		args = append(args, fields...)
	}
	return args
}

// CreateContainer 创建并后台启动容器（等价 docker run -d，返回容器短 ID）。
// 镜像本地缺失时 docker 会先自动拉取再启动，耗时不可控，超时与 Pull 同为 10 分钟。
// 调用前应先经 ValidateContainerOpts（此处二次校验兜底，与 CreateNetwork 同一双层防线）。
func (d *Dockerx) CreateContainer(opts ContainerOpts) (string, error) {
	if err := ValidateContainerOpts(opts); err != nil {
		return "", err
	}
	out, err := runTimeout(10*time.Minute, BuildRunArgs(opts)...)
	if err != nil {
		return "", fmt.Errorf("创建容器失败: %w", err)
	}
	// docker run -d 成功时 stdout 最后一行输出 64 位完整容器 ID；本地缺镜像时
	// docker 先自动拉取（"Unable to find image..." 与拉取进度走 stderr，经
	// runTimeout 的 CombinedOutput 混入输出前部），故取最后一个非空行并校验
	// hex 形态，截取前 12 位作为短 ID（与 docker ps 默认展示口径一致）。
	return extractContainerID(out), nil
}

// extractContainerID 从 docker run -d 的混合输出中提取容器 ID：
// 从最后一行向前找第一个非空且符合 12-64 位 hex 形态的行（ID 恒为输出的
// 最后一行 stdout，但 CombinedOutput 会把拉取噪音混在其后/前），截取前 12 位
// 作为短 ID；找不到返回空串（容器创建已成功，ID 仅为展示信息）。
func extractContainerID(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if isHexID(line) {
			return line[:12]
		}
	}
	return ""
}

// isHexID 判断整行是否为 12-64 位十六进制（容器 ID 的形态）。
func isHexID(s string) bool {
	if len(s) < 12 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
