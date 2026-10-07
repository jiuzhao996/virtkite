// Package dockerx 封装 docker CLI，为平台提供容器/镜像管理能力（v2 大升级批次 1）。
//
// 设计取舍：走 docker CLI + --format '{{json .}}' 结构化输出，而非 Docker Engine API——
// 零第三方依赖、天然继承用户 docker 组权限与镜像源配置。所有命令 exec.Command 数组参数，
// 禁止 shell 拼接；统一 30s 超时（logs 60s）。
package dockerx

import (
	"os"
	"path/filepath"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"sort"
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

// Pause 暂停容器（冻结进程不释放资源；前端按 State=paused 区分暂停/恢复按钮）。
func (d *Dockerx) Pause(id string) error { _, err := run("pause", id); return err }

// Unpause 恢复被暂停的容器。
func (d *Dockerx) Unpause(id string) error { _, err := run("unpause", id); return err }

// Remove 删除容器（须先停止，强制场景前端二次确认）。
func (d *Dockerx) Remove(id string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	_, err := run(append(args, id)...)
	return err
}

// Logs 返回容器尾部日志；timestamps 为真时每行前置 RFC3339Nano 时间戳。
func (d *Dockerx) Logs(id string, tail int, timestamps bool) (string, error) {
	if tail <= 0 || tail > 2000 {
		tail = 200
	}
	args := []string{"logs", "--tail", fmt.Sprint(tail)}
	if timestamps {
		args = append(args, "--timestamps")
	}
	out, err := runTimeout(60*time.Second, append(args, id)...)
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

// NetworkDetail 单网络的增强信息（IPAM 网段/网关，列表页卡片展示用）。
type NetworkDetail struct {
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway"`
}

// NetworkDetails 批量 inspect 多个网络（docker network inspect 支持多名称一次调用），
// 返回 name → {subnet, gateway}。host/none 等无 IPAM 的网络天然缺失该键，调用方按缺省处理。
// 供网络页卡片展示网段/网关（与 libvirt 虚拟网络卡片信息对齐）。
func (d *Dockerx) NetworkDetails(names []string) (map[string]NetworkDetail, error) {
	out := map[string]NetworkDetail{}
	if len(names) == 0 {
		return out, nil
	}
	raw, err := run(append([]string{"network", "inspect"}, names...)...)
	if err != nil {
		return nil, fmt.Errorf("检查网络详情失败: %w", err)
	}
	var arr []struct {
		Name  string `json:"Name"`
		IPAM  struct {
			Config []struct {
				Subnet  string `json:"Subnet"`
				Gateway string `json:"Gateway"`
			} `json:"Config"`
		} `json:"IPAM"`
	}
	if err := jsonUnmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("解析网络详情失败: %w", err)
	}
	for _, n := range arr {
		detail := NetworkDetail{}
		if len(n.IPAM.Config) > 0 {
			detail.Subnet = n.IPAM.Config[0].Subnet
			detail.Gateway = n.IPAM.Config[0].Gateway
		}
		out[n.Name] = detail
	}
	return out, nil
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

// ComposeUpFromFile 在指定项目目录执行 docker compose up -d（compose 文件固定为
// 目录内 docker-compose.yml；项目名默认=目录名，与 ComposeAction(-p) 的管理闭环对齐：
// 栈部署后即可在容器页「编排」tab 停止/下线）。镜像拉取耗时不可控，超时单独放宽到 10 分钟。
func (d *Dockerx) ComposeUpFromFile(projectDir string) error {
	if !safeDockerName(filepath.Base(projectDir)) {
		return errors.New("项目目录名非法（仅允许字母数字与 -_.）")
	}
	if _, err := os.Stat(filepath.Join(projectDir, "docker-compose.yml")); err != nil {
		return fmt.Errorf("compose 文件不存在: %w", err)
	}
	args := []string{"compose", "--project-directory", projectDir, "up", "-d"}
	if _, err := runTimeout(10*time.Minute, args...); err != nil {
		return fmt.Errorf("栈部署失败（compose up）: %w", err)
	}
	return nil
}

// ComposeService compose 服务条目（docker compose ps -a --format json）。
// Publishers 在部分版本是对象数组（含 PublishedPort），统一转成字符串便于前端直显。
type ComposeService struct {
	Service  string `json:"Service"`
	Name     string `json:"Name"`
	Image    string `json:"Image"`
	State    string `json:"State"`
	Status   string `json:"Status"`
	Ports    string `json:"Ports"`
	ID       string `json:"ID"`
	ExitCode int    `json:"ExitCode"`
}

// ComposeServices 列出 compose 项目的服务容器（docker compose -p <project> ps -a）。
func (d *Dockerx) ComposeServices(project string) ([]ComposeService, error) {
	if !safeDockerName(project) {
		return nil, errors.New("项目名非法（仅允许字母数字与 -_.）")
	}
	out, err := runTimeout(30*time.Second, "compose", "-p", project, "ps", "-a", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("获取服务列表失败: %w", err)
	}
	list := []ComposeService{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var s ComposeService
		if err := jsonUnmarshal(line, &s); err != nil {
			// Port 字段在部分 compose 版本是对象数组，反序列化失败会整行丢弃——
			// 服务列表的端口只是展示项，宁可丢端口也不能丢整行，故回退为 map 解析
			var m map[string]interface{}
			if jsonUnmarshal(line, &m) != nil {
				continue
			}
			s = ComposeService{
				Service: stringField(m, "Service"),
				Name:    stringField(m, "Name"),
				Image:   stringField(m, "Image"),
				State:   stringField(m, "State"),
				Status:  stringField(m, "Status"),
				ID:      stringField(m, "ID"),
			}
		}
		list = append(list, s)
	}
	return list, nil
}

// stringField 从 map 取字符串字段（缺失或非字符串返回空串）。
func stringField(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// composeServiceActions 服务级操作白名单（只放开 restart：单服务 stop/start 语义易与
// 项目级操作混淆，先给最小可用面）。
var composeServiceActions = map[string]bool{"restart": true}

// ComposeServiceAction 对 compose 项目内单个服务执行操作（docker compose -p <project> <action> <service>）。
func (d *Dockerx) ComposeServiceAction(project, service, action string) error {
	if !safeDockerName(project) || !safeDockerName(service) {
		return errors.New("项目名或服务名非法（仅允许字母数字与 -_.）")
	}
	if !composeServiceActions[action] {
		return errors.New("不支持的服务操作（restart）")
	}
	if _, err := runTimeout(2*time.Minute, "compose", "-p", project, action, service); err != nil {
		return fmt.Errorf("服务操作失败: %w", err)
	}
	return nil
}

// ComposePull 拉取 compose 项目全部镜像（docker compose --project-directory <dir> pull）。
// 大栈（ELK/Zabbix）拉镜像可达数十分钟，超时单独设为 30 分钟。
func (d *Dockerx) ComposePull(projectDir string) (string, error) {
	if !safeDockerName(filepath.Base(projectDir)) {
		return "", errors.New("项目目录名非法（仅允许字母数字与 -_.）")
	}
	if _, err := os.Stat(filepath.Join(projectDir, "docker-compose.yml")); err != nil {
		return "", fmt.Errorf("compose 文件不存在: %w", err)
	}
	out, err := runTimeout(30*time.Minute, "compose", "--project-directory", projectDir, "pull")
	if err != nil {
		return out, fmt.Errorf("拉取镜像失败: %w", err)
	}
	return out, nil
}

// ComposeConfigCheck 校验 compose 文件语法（docker compose -f <file> config -q，只问退出码）。
// 校验失败时 docker 的原生报错文本（含行号）对教学场景有价值，由调用方回显给用户。
func (d *Dockerx) ComposeConfigCheck(file string) error {
	if _, err := runTimeout(30*time.Second, "compose", "-f", file, "config", "-q"); err != nil {
		return err
	}
	return nil
}

// SafeStackID 栈 ID 白名单校验（handler 侧防路径穿越用，规则同 safeDockerName）。
func SafeStackID(s string) bool { return safeDockerName(s) }

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

// NetworkContainer docker 网络内的容器挂接（network inspect 的 Containers 项）。
type NetworkContainer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	IPv4 string `json:"ipv4"`
	MAC  string `json:"mac"`
}

// NetworkTopo docker 网络拓扑要素（网段 + 挂接容器）。
type NetworkTopo struct {
	Name       string             `json:"name"`
	Driver     string             `json:"driver"`
	Subnet     string             `json:"subnet"`
	Gateway    string             `json:"gateway"`
	Builtin    bool               `json:"builtin"`
	Containers []NetworkContainer `json:"containers"`
}

// NetworkTopology 批量 inspect 全部 docker 网络，返回网段与挂接容器（网络拓扑视图用）。
// 容器挂接取自 network inspect 的 Containers——仅运行中/暂停容器有活跃端点，停止容器不再挂接，
// 与「网络拓扑只画当前真实连接」的语义一致。
func (d *Dockerx) NetworkTopology() ([]NetworkTopo, error) {
	nets, err := d.Networks()
	if err != nil {
		return nil, err
	}
	if len(nets) == 0 {
		return []NetworkTopo{}, nil
	}
	names := make([]string, 0, len(nets))
	for _, n := range nets {
		names = append(names, n.Name)
	}
	raw, err := run(append([]string{"network", "inspect"}, names...)...)
	if err != nil {
		return nil, fmt.Errorf("检查网络失败: %w", err)
	}
	return parseNetworkTopo(raw)
}

// NetworkDetail 单个 docker 网络详情（网段 + 挂接容器）。
func (d *Dockerx) NetworkDetail(name string) (*NetworkTopo, error) {
	raw, err := run("network", "inspect", name)
	if err != nil {
		return nil, fmt.Errorf("检查网络失败: %w", err)
	}
	arr, err := parseNetworkTopo(raw)
	if err != nil {
		return nil, err
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("网络不存在: %s", name)
	}
	return &arr[0], nil
}

// parseNetworkTopo 解析 `docker network inspect` 的 JSON 输出（单网络或多网络数组）。
func parseNetworkTopo(raw string) ([]NetworkTopo, error) {
	var arr []struct {
		Name   string `json:"Name"`
		Driver string `json:"Driver"`
		IPAM   struct {
			Config []struct {
				Subnet  string `json:"Subnet"`
				Gateway string `json:"Gateway"`
			} `json:"Config"`
		} `json:"IPAM"`
		Containers map[string]struct {
			Name        string `json:"Name"`
			MacAddress  string `json:"MacAddress"`
			IPv4Address string `json:"IPv4Address"`
		} `json:"Containers"`
	}
	if err := jsonUnmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("解析网络详情失败: %w", err)
	}
	out := make([]NetworkTopo, 0, len(arr))
	for _, n := range arr {
		t := NetworkTopo{Name: n.Name, Driver: n.Driver, Builtin: builtinNetworks[n.Name]}
		if len(n.IPAM.Config) > 0 {
			t.Subnet = n.IPAM.Config[0].Subnet
			t.Gateway = n.IPAM.Config[0].Gateway
		}
		ids := make([]string, 0, len(n.Containers))
		for id := range n.Containers {
			ids = append(ids, id)
		}
		sort.Strings(ids) // 输出稳定，便于前端 diff/对比
		for _, id := range ids {
			c := n.Containers[id]
			ip := c.IPv4Address
			if i := strings.IndexByte(ip, '/'); i > 0 {
				ip = ip[:i]
			}
			t.Containers = append(t.Containers, NetworkContainer{
				ID: id, Name: c.Name, IPv4: ip, MAC: strings.ToLower(c.MacAddress),
			})
		}
		out = append(out, t)
	}
	return out, nil
}
