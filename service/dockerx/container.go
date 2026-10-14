// 本文件负责容器维度的操作：列表/来源反查、生命周期（启停/重启/暂停/恢复/删除）、
// 资源与健康检查动态调整、接入/断开网络、日志、详情/实时统计、重命名。
package dockerx

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Container 容器列表条目（docker ps --format '{{json .}}'）。
// Project/Service 来自 compose 标签（来源反查用），不在 {{json .}} 输出里，另行补取。
type Container struct {
	ID      string `json:"ID"`
	Names   string `json:"Names"`
	Image   string `json:"Image"`
	State   string `json:"State"`
	Status  string `json:"Status"`
	Ports   string `json:"Ports"`
	Created string `json:"CreatedAt"`
	// 来源反查（借 1Panel 的「关联」列）：compose 部署的容器带 com.docker.compose.project
	// 标签，据此显示所属栈名并可跳转；两者皆空即「手工创建」。
	Project string `json:"Project,omitempty"`
	Service string `json:"Service,omitempty"`
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
	// 来源反查：compose 项目 label 不在 {{json .}} 里，单独一次 ps 拉取（快）。
	// 失败静默降级——来源列是增强信息，不能因为它拉不到就把整个容器列表打掉。
	if labels, err := d.containerComposeLabels(); err == nil {
		for i := range list {
			if src, ok := labels[list[i].ID]; ok {
				list[i].Project, list[i].Service = src.Project, src.Service
			}
		}
	}
	return list, nil
}

// ContainerSource 容器来源（compose 项目名 + 服务名）。
type ContainerSource struct {
	Project string
	Service string
}

// containerComposeLabels 用一次 ps 拉全部容器的 compose 项目/服务标签。
// 用制表符分隔（容器名/项目名/服务名都不允许含制表符，安全）；ID 用短 ID，与列表条目对齐。
func (d *Dockerx) containerComposeLabels() (map[string]ContainerSource, error) {
	out, err := run("ps", "-a", "--format",
		"{{.ID}}\t{{.Label \"com.docker.compose.project\"}}\t{{.Label \"com.docker.compose.service\"}}")
	if err != nil {
		return nil, err
	}
	m := map[string]ContainerSource{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 || parts[0] == "" {
			continue
		}
		if parts[1] == "" && parts[2] == "" {
			continue // 手工创建的容器无 compose 标签
		}
		m[parts[0]] = ContainerSource{Project: parts[1], Service: parts[2]}
	}
	return m, nil
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

// UpdateContainer 动态调整容器的资源限制与健康检查（等价 docker update，运行中亦可）。
// 只输出非空项——docker update 未给的 flag 不动原值。
func (d *Dockerx) UpdateContainer(id string, opts ContainerOpts) error {
	if err := ValidateRuntimeLimits(opts); err != nil {
		return err
	}
	args := []string{"update"}
	for _, kv := range []struct{ flag, val string }{
		{"--cpus", opts.CPUs},
		{"--memory", opts.Memory},
		{"--memory-swap", opts.MemorySwap},
		{"--cpuset-cpus", opts.CpusetCpus},
		{"--health-interval", opts.HealthInterval},
		{"--health-retries", opts.HealthRetries},
		{"--health-timeout", opts.HealthTimeout},
		{"--health-start-period", opts.HealthStartPeriod},
	} {
		if kv.val != "" {
			args = append(args, kv.flag, kv.val)
		}
	}
	if len(args) == 1 {
		return errors.New("没有可更新的项（资源限制或健康检查参数至少填一个）")
	}
	_, err := run(append(args, id)...)
	return err
}

// ConnectNetwork 把容器接入指定网络（等价 docker network connect）。
func (d *Dockerx) ConnectNetwork(network, container string) error {
	_, err := run("network", "connect", network, container)
	return err
}

// DisconnectNetwork 把容器从指定网络断开（等价 docker network disconnect）。
func (d *Dockerx) DisconnectNetwork(network, container string) error {
	_, err := run("network", "disconnect", network, container)
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
