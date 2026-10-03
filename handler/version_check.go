// version_check.go：镜像版本检测（P3，2026-10-04）——本地镜像 digest vs
// 镜像代理远端同名 tag 的 digest 对比，不一致 = 远端有更新。
//
// GET /api/docker/version-check（operator+，前端按钮触发非轮询——外网请求秒级）
//
// 通道：docker.m.daocloud.io（镜像加速器，registry v2 协议透传；本机实测
// token realm 两步流可用、digest 稳定返回；tags/list 被上游禁用故走 digest 对比）。
// 已知限制：digest 变化也可能源于基础层重建（同版本重推），文案以「远端有更新/重建」
// 表述不夸大为「新版本」；host/none 等无远端对应的镜像（scratched/本地构建）报 skipped。
package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
)

// VersionCheckHandler 镜像版本检测处理器。
type VersionCheckHandler struct {
	Docker *dockerx.Dockerx
}

// NewVersionCheckHandler 创建版本检测处理器。
func NewVersionCheckHandler() *VersionCheckHandler {
	return &VersionCheckHandler{Docker: dockerx.New()}
}

const (
	registryBase   = "https://docker.m.daocloud.io"
	registryToken  = "https://m.daocloud.io/auth/token?service=docker.m.daocloud.io&scope=repository:%s:pull"
	versionTimeout = 6 * time.Second
)

var vcClient = &http.Client{Timeout: versionTimeout}

// vcItem 单镜像的检测结果。
type vcItem struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	LocalDigest  string `json:"local_digest"`
	RemoteDigest string `json:"remote_digest"`
	Status     string `json:"status"` // up_to_date | outdated | skipped | unknown
	Note       string `json:"note,omitempty"`
}

// localDigest 读取本地镜像的 RepoDigests（docker inspect；无 RepoDigests=本地构建/导入）。
func (h *VersionCheckHandler) localDigest(image string) string {
	out, err := h.Docker.Inspect(image)
	if err != nil {
		return ""
	}
	// dockerx.Inspect 返回 docker inspect 的 [0]：RepoDigests 形如
	// ["nginx@sha256:...", "nginx:1.27@sha256:..."]——取任一含 @sha256 的条目。
	if arr, ok := out["RepoDigests"].([]interface{}); ok {
		for _, d := range arr {
			if s, ok := d.(string); ok && strings.Contains(s, "@sha256:") {
				return s[strings.Index(s, "@sha256:")+len("@sha256:"):]
			}
		}
	}
	return ""
}

// remoteDigest 经镜像代理查远端 manifest digest（两步 token 流）。
func (h *VersionCheckHandler) remoteDigest(repo, tag string) (string, error) {
	// scope 包装在 registryToken 模板内（repository:%s:pull）；'/' 原样传——
	// m.daocloud 对 %2F 编码形态返回 403
	tr, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf(registryToken, repo), nil)
	if err != nil {
		return "", err
	}
	resp, err := vcClient.Do(tr)
	if err != nil {
		return "", fmt.Errorf("token 获取失败: %w", err)
	}
	defer resp.Body.Close()
	var tok struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(resp.Body).Decode(&tok) != nil || tok.Token == "" {
		return "", fmt.Errorf("token 响应异常")
	}

	mr, err := http.NewRequest(http.MethodHead,
		fmt.Sprintf("%s/v2/%s/manifests/%s", registryBase, repo, tag), nil)
	if err != nil {
		return "", err
	}
	mr.Header.Set("Authorization", "Bearer "+tok.Token)
	// 多 manifest 版本都接受：digest 对比不关心 schema 差异，只要远端稳定返回
	mr.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json")
	resp2, err := vcClient.Do(mr)
	if err != nil {
		return "", fmt.Errorf("manifest 查询失败: %w", err)
	}
	defer resp2.Body.Close()
	io.Copy(io.Discard, resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		return "", fmt.Errorf("manifest 响应 %d", resp2.StatusCode)
	}
	digest := resp2.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("响应无 digest")
	}
	return digest, nil
}

// vcRepoTag 把本地镜像名拆 repo/tag（无 tag 默认 latest；带 registry host 的
// 含端口形式如 localhost:5000/x:1 也正确切分）。
var repoTagRe = regexp.MustCompile(`^(.+)/([^/]+):([^/]+)$`)

func splitRepoTag(image string) (repo, tag string) {
	if i := strings.LastIndex(image, ":"); i > -1 && !strings.Contains(image[i:], "/") {
		return image[:i], image[i+1:]
	}
	return image, "latest"
}

// Check GET /api/docker/version-check —— 全部本地镜像批量检测。
func (h *VersionCheckHandler) Check(c *gin.Context) {
	images, err := h.Docker.Images()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取本地镜像失败", err)
		return
	}
	items := make([]vcItem, 0, len(images))
	for _, im := range images {
		image := im.Repository + ":" + im.Tag
		item := vcItem{Repository: im.Repository, Tag: im.Tag}
		if im.Repository == "<none>" || im.Tag == "<none>" {
			item.Status = "skipped"
			item.Note = "悬空镜像，无远端对应"
			items = append(items, item)
			continue
		}
		repo, tag := splitRepoTag(image)
		local := h.localDigest(image)
		if local == "" {
			item.Status = "skipped"
			item.Note = "本地构建/导入镜像（无 RepoDigest）"
			items = append(items, item)
			continue
		}
		item.LocalDigest = shortDigest(local)
		remote, err := h.remoteDigest(repo, tag)
		if err != nil {
			item.Status = "unknown"
			item.Note = "远端查询失败（" + err.Error() + "）"
			items = append(items, item)
			continue
		}
		item.RemoteDigest = shortDigest(remote)
		if strings.HasPrefix(local, remote) || strings.HasPrefix(remote, local) {
			item.Status = "up_to_date"
		} else {
			item.Status = "outdated"
			item.Note = "远端镜像有更新/重建（digest 不一致）"
		}
		items = append(items, item)
	}
	Success(c, gin.H{"items": items, "total": len(items), "checked_at": time.Now().Format("15:04:05")})
}

func shortDigest(d string) string {
	if len(d) > 19 {
		return d[:19] + "…"
	}
	return d
}
