// Package dockerx 封装 docker CLI，为平台提供容器/镜像管理能力（v2 大升级批次 1）。
//
// 设计取舍：走 docker CLI + --format '{{json .}}' 结构化输出，而非 Docker Engine API——
// 零第三方依赖、天然继承用户 docker 组权限与镜像源配置。所有命令 exec.Command 数组参数，
// 禁止 shell 拼接；统一 30s 超时（logs 60s）。
//
// 文件按职责拆分（纯搬运，无行为变更）：本文件是封装入口与底层命令执行/通用校验助手；
// 容器见 container.go；镜像见 image.go；网络见 network.go；卷见 volume.go；
// compose 编排见 compose.go；镜像构建见 build.go。
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

// SafeStackID 栈 ID 白名单校验（handler 侧防路径穿越用，规则同 safeDockerName）。
func SafeStackID(s string) bool { return safeDockerName(s) }

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
