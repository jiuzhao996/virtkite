// 本文件负责镜像维度的操作：列表、拉取、删除/清理悬空镜像、标签校验与镜像标签白名单，
// 以及 docker CLI 可用性探测。容器清理（container prune）的回收空间解析 ReclaimedSpace 也在此。
package dockerx

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

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

// imageTagRe 镜像标签白名单（name[:tag]，允许 registry 前缀里的 / 与 .）。
// 首字符必须字母数字——参数是以数组传给 docker 的（无 shell），但 `-t --help` 这种
// 以 - 开头的值仍会被 docker 当成 flag 解析，故必须挡在 exec 之前。
var imageTagRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,127}(:[a-zA-Z0-9][a-zA-Z0-9._-]{0,127})?$`)

// ValidateTag 校验镜像标签（Build 与 handler 共用）。
func ValidateTag(tag string) error {
	if !imageTagRe.MatchString(tag) {
		return errors.New("镜像标签非法（如 myapp:1.0，字母数字开头，可含 . _ - / 与一个 :tag）")
	}
	return nil
}
