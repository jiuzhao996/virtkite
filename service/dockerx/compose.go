// 本文件负责 docker compose 编排：项目列表/操作（up/start/stop/restart/down）、服务列表与
// 单服务操作、按目录 up、拉取项目镜像、compose 文件语法校验。所有栈级操作共享 tryStackLock
// 串行化（见 stacklock.go），项目名/目录名一律过 safeDockerName 白名单。
package dockerx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
	// 栈级锁：同一栈串行、异栈并行（借 Dockge 终端名唯一性的思路）
	release, ok := tryStackLock(project)
	if !ok {
		return ErrStackBusy
	}
	defer release()
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
	// 栈级锁：项目名 = 目录名，与 ComposeAction(-p) 共享同一把锁，两条入口互相串行
	release, ok := tryStackLock(filepath.Base(projectDir))
	if !ok {
		return ErrStackBusy
	}
	defer release()
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
	// 服务级操作同样锁整个栈：避免「重启某服务」与「整栈 up/down」并发互相踩
	release, ok := tryStackLock(project)
	if !ok {
		return ErrStackBusy
	}
	defer release()
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
	// 栈级锁：与 up/action 共享同一把锁，拉镜像期间禁止同栈其它操作
	release, ok := tryStackLock(filepath.Base(projectDir))
	if !ok {
		return "", ErrStackBusy
	}
	defer release()
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
