// 本文件负责 docker 卷维度的操作：卷列表、创建/删除卷、清理未被容器占用的卷。
package dockerx

import (
	"errors"
	"fmt"
	"strings"
)

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
