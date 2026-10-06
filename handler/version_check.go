// version_check.go：镜像版本检测端点（P3，2026-10-04）——核心逻辑在 service/imgver
// （R9 抽出：handler 与 cron 共用，cron 直接 import handler 会成环）。
//
// GET /api/docker/version-check（operator+，前端按钮触发非轮询——外网请求秒级）
package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/imgver"
)

// VersionCheckHandler 镜像版本检测处理器。
type VersionCheckHandler struct {
	Docker *dockerx.Dockerx
}

// NewVersionCheckHandler 创建版本检测处理器。
func NewVersionCheckHandler() *VersionCheckHandler {
	return &VersionCheckHandler{Docker: dockerx.New()}
}

// newImgver 组装 imgver.Runner（注入本地镜像列举与 inspect 能力）。
func (h *VersionCheckHandler) newImgver() *imgver.Runner {
	return &imgver.Runner{
		ListImages: func() ([][2]string, error) {
			imgs, err := h.Docker.Images()
			if err != nil {
				return nil, err
			}
			out := make([][2]string, 0, len(imgs))
			for _, im := range imgs {
				out = append(out, [2]string{im.Repository, im.Tag})
			}
			return out, nil
		},
		LocalDigest: func(image string) string {
			info, err := h.Docker.Inspect(image)
			if err != nil {
				return ""
			}
			// RepoDigests 形如 ["nginx@sha256:...", "nginx:1.27@sha256:..."]
			if arr, ok := info["RepoDigests"].([]interface{}); ok {
				for _, d := range arr {
					if s, ok := d.(string); ok && strings.Contains(s, "@sha256:") {
						return s[strings.Index(s, "@sha256:")+len("@sha256:"):]
					}
				}
			}
			return ""
		},
	}
}

// Check GET /api/docker/version-check —— 全部本地镜像批量检测。
func (h *VersionCheckHandler) Check(c *gin.Context) {
	items, err := h.newImgver().CheckAll()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "获取本地镜像失败", err)
		return
	}
	Success(c, gin.H{"items": items, "total": len(items), "checked_at": time.Now().Format("15:04:05")})
}
