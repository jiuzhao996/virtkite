// Package dockerx 封装 docker CLI，为平台提供容器/镜像管理能力（v2 大升级批次 1）。
//
// 设计取舍：走 docker CLI + --format '{{json .}}' 结构化输出，而非 Docker Engine API——
// 零第三方依赖、天然继承用户 docker 组权限与镜像源配置。所有命令 exec.Command 数组参数，
// 禁止 shell 拼接；统一 30s 超时（logs 60s）。
package dockerx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

// Dockerx docker CLI 封装（无状态，可直接构造）。
type Dockerx struct{}

// New 创建 Dockerx。
func New() *Dockerx { return &Dockerx{} }

func run(args ...string) (string, error) {
	return runTimeout(30*time.Second, args...)
}

func runTimeout(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("docker 命令超时: %w", err)
		}
		if msg != "" {
			return "", fmt.Errorf("%s: %w", msg, err)
		}
		return "", fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Container 容器列表条目（docker ps --format '{{json .}}'）。
type Container struct {
	ID      string `json:"ID"`
	Names   string `json:"Names"`
	Image   string `json:"Image"`
	State   string `json:"State"`
	Status  string `json:"Status"`
	Ports   string `json:"Ports"`
	Created string `json:"CreatedAt"`
}

// Containers 返回全部容器（含停止）。
func (d *Dockerx) Containers() ([]Container, error) {
	out, err := run("ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	list := []Container{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c Container
		if err := jsonUnmarshal(line, &c); err != nil {
			continue // 跳过无法解析行，不让单行脏数据毁掉整表
		}
		list = append(list, c)
	}
	return list, nil
}

// Start 启动容器。
func (d *Dockerx) Start(id string) error { _, err := run("start", id); return err }

// Stop 停止容器。
func (d *Dockerx) Stop(id string) error { _, err := run("stop", id); return err }

// Restart 重启容器。
func (d *Dockerx) Restart(id string) error { _, err := run("restart", id); return err }

// Remove 删除容器（须先停止，强制场景前端二次确认）。
func (d *Dockerx) Remove(id string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	_, err := run(append(args, id)...)
	return err
}

// Logs 返回容器尾部日志。
func (d *Dockerx) Logs(id string, tail int) (string, error) {
	if tail <= 0 || tail > 2000 {
		tail = 200
	}
	out, err := runTimeout(60*time.Second, "logs", "--tail", fmt.Sprint(tail), id)
	if err != nil {
		return "", err
	}
	return out, nil
}

// Image 镜像列表条目。
type Image struct {
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
	ID         string `json:"ID"`
	Size       string `json:"Size"`
	Created    string `json:"CreatedSince"`
}

// Images 返回本机镜像列表。
func (d *Dockerx) Images() ([]Image, error) {
	out, err := run("images", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	list := []Image{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var img Image
		if err := jsonUnmarshal(line, &img); err != nil {
			continue
		}
		list = append(list, img)
	}
	return list, nil
}

// RemoveImage 删除镜像。
func (d *Dockerx) RemoveImage(id string) error { _, err := run("rmi", id); return err }

// Available 探测 docker CLI 是否可用（探测走 docker version --format，不依赖守护进程外的资源）。
func (d *Dockerx) Available() (bool, string) {
	out, err := run("version", "--format", "{{.Server.Version}}")
	if err != nil {
		return false, err.Error()
	}
	return true, strings.TrimSpace(out)
}

// ─────────────── 容器详情 / 实时统计 / 重命名（v3.2 批次 R） ───────────────

// Inspect 容器完整详情（等价 docker inspect <id>），JSON 数组取 [0]，对象原样透传给前端渲染。
func (d *Dockerx) Inspect(id string) (map[string]interface{}, error) {
	out, err := run("inspect", id)
	if err != nil {
		return nil, fmt.Errorf("容器不存在或无法检查: %w", err)
	}
	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		return nil, fmt.Errorf("解析 inspect 输出失败: %w", err)
	}
	if len(arr) == 0 {
		return nil, errors.New("inspect 输出为空")
	}
	return arr[0], nil
}

// Stats 单容器实时资源占用（等价 docker stats --no-stream <id>；--no-stream 采一次即退，
// 避免常驻流式连接挂住 CLI 进程）。容器未运行时 docker 不输出行，视为无数据。
func (d *Dockerx) Stats(id string) (map[string]interface{}, error) {
	out, err := run("stats", "--no-stream", "--format", "{{json .}}", id)
	if err != nil {
		return nil, fmt.Errorf("获取容器统计失败: %w", err)
	}
	list := parseJSONMaps(out)
	if len(list) == 0 {
		return nil, errors.New("容器未运行，无统计数据")
	}
	return list[0], nil
}

// StatsAll 全部容器实时资源占用（等价 docker stats --no-stream，列表页资源列轮询用）。
func (d *Dockerx) StatsAll() ([]map[string]interface{}, error) {
	out, err := run("stats", "--no-stream", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("获取容器统计失败: %w", err)
	}
	return parseJSONMaps(out), nil
}

// RenameContainer 重命名容器（等价 docker rename，运行中容器也允许）。
func (d *Dockerx) RenameContainer(id, newName string) error {
	if !safeDockerName(id) || !safeDockerName(newName) {
		return errors.New("容器 ID 或新名称非法（仅允许字母数字与 -_.）")
	}
	if _, err := run("rename", id, newName); err != nil {
		return fmt.Errorf("重命名容器失败: %w", err)
	}
	return nil
}

// ─────────────── 镜像拉取 / 清理 ───────────────

// Pull 拉取镜像（等价 docker pull）。镜像分层下载耗时不可控，超时放宽到 10 分钟；
// 返回原始输出，由 handler 截取尾部给前端。
func (d *Dockerx) Pull(nameTag string) (string, error) {
	if !safeImageRef(nameTag) {
		return "", errors.New("镜像名非法（仅允许字母数字与 .:_-/@）")
	}
	out, err := runTimeout(10*time.Minute, "pull", nameTag)
	if err != nil {
		return "", fmt.Errorf("镜像拉取失败: %w", err)
	}
	return out, nil
}

// PruneImages 清理悬空镜像（等价 docker image prune -f，不带 -a 不动在用镜像），返回原始输出。
func (d *Dockerx) PruneImages() (string, error) {
	out, err := run("image", "prune", "-f")
	if err != nil {
		return "", fmt.Errorf("清理悬空镜像失败: %w", err)
	}
	return out, nil
}

// PruneContainers 清理全部已停止容器（等价 docker container prune -f），返回原始输出。
func (d *Dockerx) PruneContainers() (string, error) {
	out, err := run("container", "prune", "-f")
	if err != nil {
		return "", fmt.Errorf("清理已停止容器失败: %w", err)
	}
	return out, nil
}

// ReclaimedSpace 从 prune 输出提取「Total reclaimed space:」后的空间值（无则空串），
// 供 handler 拼中文提示。输出样例末行：Total reclaimed space: 25.91MB
func ReclaimedSpace(out string) string {
	const key = "Total reclaimed space:"
	i := strings.LastIndex(out, key)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(out[i+len(key):])
}

// ─────────────── 网络 ───────────────

// Network 网络列表条目（docker network ls --format '{{json .}}'，IPv6/Internal 为 "true"/"false" 字符串）。
type Network struct {
	Name      string `json:"Name"`
	Driver    string `json:"Driver"`
	Scope     string `json:"Scope"`
	IPv6      string `json:"IPv6"`
	Internal  string `json:"Internal"`
	CreatedAt string `json:"CreatedAt"`
}

// Networks 返回 docker 网络列表（等价 docker network ls）。
func (d *Dockerx) Networks() ([]Network, error) {
	out, err := run("network", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("获取网络列表失败: %w", err)
	}
	list := []Network{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var n Network
		if err := jsonUnmarshal(line, &n); err != nil {
			continue // 跳过无法解析行，不让单行脏数据毁掉整表
		}
		list = append(list, n)
	}
	return list, nil
}

// networkDrivers docker network create -d 的驱动白名单。
var networkDrivers = map[string]bool{
	"bridge":  true,
	"overlay": true,
	"macvlan": true,
	"ipvlan":  true,
	"host":    true,
	"none":    true,
}

// builtinNetworks docker 内置网络黑名单——删除一律拒绝（与 libvirt 删卷守卫同一立场：
// 宁可删不掉，不可弄坏宿主底座）。docker_gwbridge 是 overlay 的隐藏网关桥，同样不可删。
var builtinNetworks = map[string]bool{
	"bridge":          true,
	"host":            true,
	"none":            true,
	"docker_gwbridge": true,
}

// CreateNetwork 创建自定义网络（等价 docker network create）。subnet/gateway 可空，
// 提供时必须分别是合法 CIDR 与 IP——数组参数拼接本无注入风险，这里挡的是明显误输入。
func (d *Dockerx) CreateNetwork(name, driver, subnet, gateway string) error {
	if !safeDockerName(name) {
		return errors.New("网络名非法（仅允许字母数字与 -_.）")
	}
	if driver == "" {
		driver = "bridge"
	}
	if !networkDrivers[driver] {
		return errors.New("网络驱动不支持（bridge/overlay/macvlan/ipvlan/host/none）")
	}
	if subnet != "" {
		if _, _, err := net.ParseCIDR(subnet); err != nil {
			return errors.New("subnet 必须是合法 CIDR，如 172.30.0.0/16")
		}
	}
	if gateway != "" && net.ParseIP(gateway) == nil {
		return errors.New("gateway 必须是合法 IP 地址")
	}
	args := []string{"network", "create", "-d", driver}
	if subnet != "" {
		args = append(args, "--subnet", subnet)
	}
	if gateway != "" {
		args = append(args, "--gateway", gateway)
	}
	if _, err := run(append(args, name)...); err != nil {
		return fmt.Errorf("创建网络失败: %w", err)
	}
	return nil
}

// RemoveNetwork 删除自定义网络（等价 docker network rm），内置网络直接拒绝。
func (d *Dockerx) RemoveNetwork(name string) error {
	if !safeDockerName(name) {
		return errors.New("网络名非法（仅允许字母数字与 -_.）")
	}
	if builtinNetworks[name] {
		return errors.New("内置网络不允许删除")
	}
	if _, err := run("network", "rm", name); err != nil {
		return fmt.Errorf("删除网络失败: %w", err)
	}
	return nil
}

// ─────────────── 卷 ───────────────

// Volume 卷列表条目（docker volume ls --format '{{json .}}'）。
type Volume struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Scope  string `json:"Scope"`
}

// Volumes 返回 docker 卷列表（等价 docker volume ls）。
func (d *Dockerx) Volumes() ([]Volume, error) {
	out, err := run("volume", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("获取卷列表失败: %w", err)
	}
	list := []Volume{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v Volume
		if err := jsonUnmarshal(line, &v); err != nil {
			continue
		}
		list = append(list, v)
	}
	return list, nil
}

// CreateVolume 创建卷（等价 docker volume create，驱动默认 local）。
func (d *Dockerx) CreateVolume(name string) error {
	if !safeDockerName(name) {
		return errors.New("卷名非法（仅允许字母数字与 -_.）")
	}
	if _, err := run("volume", "create", name); err != nil {
		return fmt.Errorf("创建卷失败: %w", err)
	}
	return nil
}

// RemoveVolume 删除卷（等价 docker volume rm；被容器占用的卷由 docker 侧报错拒绝，不会误删）。
func (d *Dockerx) RemoveVolume(name string) error {
	if !safeDockerName(name) {
		return errors.New("卷名非法（仅允许字母数字与 -_.）")
	}
	if _, err := run("volume", "rm", name); err != nil {
		return fmt.Errorf("删除卷失败: %w", err)
	}
	return nil
}

// PruneVolumes 清理未被容器占用的卷（等价 docker volume prune -f），返回原始输出。
// 在用卷由 docker 自身拒绝，与 libvirt 删卷守卫同一立场。
func (d *Dockerx) PruneVolumes() (string, error) {
	out, err := run("volume", "prune", "-f")
	if err != nil {
		return "", fmt.Errorf("清理卷失败: %w", err)
	}
	return out, nil
}

// ─────────────── compose 编排 ───────────────

// ComposeProject compose 项目列表条目（docker compose ls --format json）。
type ComposeProject struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	ConfigFiles string `json:"ConfigFiles"`
}

// ComposeList 返回 compose 项目列表。compose v2 多数版本输出 JSON 数组，
// 少数版本输出 JSON Lines（每行一个对象），两种格式都兼容。
func (d *Dockerx) ComposeList() ([]ComposeProject, error) {
	out, err := run("compose", "ls", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("获取 compose 项目失败: %w", err)
	}
	trimmed := strings.TrimSpace(out)
	if strings.HasPrefix(trimmed, "[") {
		list := []ComposeProject{}
		if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
			return nil, fmt.Errorf("解析 compose ls 输出失败: %w", err)
		}
		return list, nil
	}
	list := []ComposeProject{}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p ComposeProject
		if err := jsonUnmarshal(line, &p); err != nil {
			continue
		}
		list = append(list, p)
	}
	return list, nil
}

// composeActions 项目操作白名单 → docker compose 子命令（up 带 -d 后台运行）。
var composeActions = map[string][]string{
	"up":      {"up", "-d"},
	"start":   {"start"},
	"stop":    {"stop"},
	"restart": {"restart"},
	"down":    {"down"},
}

// ComposeAction 对 compose 项目执行操作（等价 docker compose -p <project> <action>）。
// stop/start/restart/down 凭容器 label 发现项目，无需 compose 文件；
// up -d 需要 CWD（或项目目录）存在 docker-compose.yml。项目级操作可能重建多个容器，超时 2 分钟。
func (d *Dockerx) ComposeAction(project, action string) error {
	if !safeDockerName(project) {
		return errors.New("项目名非法（仅允许字母数字与 -_.）")
	}
	sub, ok := composeActions[action]
	if !ok {
		return errors.New("不支持的 compose 操作（up/start/stop/restart/down）")
	}
	args := append([]string{"compose", "-p", project}, sub...)
	if _, err := runTimeout(2*time.Minute, args...); err != nil {
		return fmt.Errorf("compose 项目操作失败: %w", err)
	}
	return nil
}

// ─────────────── 内部校验 / 解析助手 ───────────────

// safeDockerName 名称白名单（与 handler.safeDockerID 同规则）：字母数字、下划线、连字符、点。
// service 层不 import handler，两处各自定义，改一处必须同步另一处（同 virt.Status* 与 model.VMStatus* 惯例）。
func safeDockerName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

// safeImageRef 镜像引用白名单：在名称字符基础上放行 :（tag/仓库端口）、/（仓库路径）、@（digest）。
func safeImageRef(ref string) bool {
	if ref == "" || len(ref) > 256 {
		return false
	}
	for _, r := range ref {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.' || r == ':' || r == '/' || r == '@'
		if !ok {
			return false
		}
	}
	return true
}

// parseJSONMaps 逐行解析 '{{json .}}' 输出为 map 切片，跳过无法解析的脏行。
func parseJSONMaps(out string) []map[string]interface{} {
	list := []map[string]interface{}{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := jsonUnmarshal(line, &m); err != nil {
			continue
		}
		list = append(list, m)
	}
	return list
}
