// Package dockerx 封装 docker CLI，为平台提供容器/镜像管理能力（v2 大升级批次 1）。
//
// 设计取舍：走 docker CLI + --format '{{json .}}' 结构化输出，而非 Docker Engine API——
// 零第三方依赖、天然继承用户 docker 组权限与镜像源配置。所有命令 exec.Command 数组参数，
// 禁止 shell 拼接；统一 30s 超时（logs 60s）。
package dockerx

import (
	"context"
	"fmt"
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
