// Package imgver 镜像版本检测：本地镜像 digest vs 镜像代理远端同名 tag 的 digest 对比，
// 不一致 = 远端有更新/重建。
//
// 从 handler/version_check.go 抽出（R9）：handler 与 cron 都要调用，cron 直接 import
// handler 会形成环（handler/crons.go 已 import service/cron），故核心逻辑下沉到此包。
//
// 通道：docker.m.daocloud.io（镜像加速器，registry v2 协议透传）。已知限制：digest 变化
// 也可能源于基础层重建（同版本重推），文案以「远端有更新/重建」表述不夸大为「新版本」。
package imgver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	registryBase   = "https://docker.m.daocloud.io"
	registryToken  = "https://m.daocloud.io/auth/token?service=docker.m.daocloud.io&scope=repository:%s:pull"
	versionTimeout = 6 * time.Second
)

var client = &http.Client{Timeout: versionTimeout}

// Item 单镜像的检测结果。
type Item struct {
	Repository   string `json:"repository"`
	Tag          string `json:"tag"`
	LocalDigest  string `json:"local_digest"`
	RemoteDigest string `json:"remote_digest"`
	Status       string `json:"status"` // up_to_date | outdated | skipped | unknown
	Note         string `json:"note,omitempty"`
}

// Runner 注入本地镜像列举与 inspect 能力（handler 传 dockerx.Dockerx）。
// 用接口而非直接依赖 dockerx：便于单测注入假件。
type Runner struct {
	// ListImages 返回 (repository, tag) 列表
	ListImages func() ([][2]string, error)
	// LocalDigest 返回本地镜像的 RepoDigest（无则空串）
	LocalDigest func(image string) string
}

// CheckAll 对全部本地镜像批量检测，返回结果与统计。
func (r *Runner) CheckAll() ([]Item, error) {
	imgs, err := r.ListImages()
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(imgs))
	for _, im := range imgs {
		repo, tag := im[0], im[1]
		image := repo + ":" + tag
		item := Item{Repository: repo, Tag: tag}
		if repo == "<none>" || tag == "<none>" {
			item.Status = "skipped"
			item.Note = "悬空镜像，无远端对应"
			items = append(items, item)
			continue
		}
		splitRepo, splitTag := SplitRepoTag(image)
		local := r.LocalDigest(image)
		if local == "" {
			item.Status = "skipped"
			item.Note = "本地构建/导入镜像（无 RepoDigest）"
			items = append(items, item)
			continue
		}
		item.LocalDigest = ShortDigest(local)
		remote, rerr := remoteDigests(splitRepo, splitTag)
		if rerr != nil {
			item.Status = "unknown"
			item.Note = "远端查询失败（" + rerr.Error() + "）"
			items = append(items, item)
			continue
		}
		item.RemoteDigest = ShortDigest(remote[0])
		localHex := strings.TrimPrefix(local, "sha256:")
		match := false
		for _, dg := range remote {
			if strings.TrimPrefix(dg, "sha256:") == localHex {
				match = true
				break
			}
		}
		if match {
			item.Status = "up_to_date"
		} else {
			item.Status = "outdated"
			item.Note = "远端镜像有更新/重建（digest 有差异）"
		}
		items = append(items, item)
	}
	return items, nil
}

// Outdated 返回 outdated 状态的镜像名（repo:tag）列表。
func Outdated(items []Item) []string {
	var out []string
	for _, it := range items {
		if it.Status == "outdated" {
			out = append(out, it.Repository+":"+it.Tag)
		}
	}
	return out
}

// remoteDigests 经镜像代理查远端 manifest（两步 token 流），返回该 tag 的 digest 集合。
func remoteDigests(repo, tag string) ([]string, error) {
	tr, err := http.NewRequest(http.MethodGet, fmt.Sprintf(registryToken, repo), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(tr)
	if err != nil {
		return nil, fmt.Errorf("token 获取失败: %w", err)
	}
	defer resp.Body.Close()
	var tok struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(resp.Body).Decode(&tok) != nil || tok.Token == "" {
		return nil, fmt.Errorf("token 响应异常")
	}

	mr, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v2/%s/manifests/%s", registryBase, repo, tag), nil)
	if err != nil {
		return nil, err
	}
	mr.Header.Set("Authorization", "Bearer "+tok.Token)
	mr.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json")
	resp2, err := client.Do(mr)
	if err != nil {
		return nil, fmt.Errorf("manifest 查询失败: %w", err)
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest 响应 %d", resp2.StatusCode)
	}
	digests := []string{}
	if dg := resp2.Header.Get("Docker-Content-Digest"); dg != "" {
		digests = append(digests, dg)
	}
	var parsed struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		for _, m := range parsed.Manifests {
			if m.Digest != "" {
				digests = append(digests, m.Digest)
			}
		}
	}
	if len(digests) == 0 {
		return nil, fmt.Errorf("响应无 digest")
	}
	return digests, nil
}

// SplitRepoTag 把本地镜像名拆 repo/tag（无 tag 默认 latest）。
func SplitRepoTag(image string) (repo, tag string) {
	if i := strings.LastIndex(image, ":"); i > -1 && !strings.Contains(image[i:], "/") {
		return image[:i], image[i+1:]
	}
	return image, "latest"
}

// ShortDigest 截短 digest 供展示。
func ShortDigest(d string) string {
	if len(d) > 19 {
		return d[:19] + "…"
	}
	return d
}
