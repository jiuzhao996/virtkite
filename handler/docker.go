// docker.go：容器/镜像管理（v2 大升级批次 1，对标宝塔的「服务管理」心智）——核心装配。
// 处理器装配、docker 可用性探测与 ID 白名单。容器见 docker_containers.go，镜像见
// docker_images.go，网络见 docker_networks.go，卷见 docker_volumes.go，compose 见 docker_compose.go。
package handler

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/tasks"
)

// statsAllCacheTTL 全容器资源列缓存时长（见 StatsAll）。
const statsAllCacheTTL = 3 * time.Second

// DockerHandler 容器/镜像管理（v2 大升级批次 1，对标宝塔的「服务管理」心智）。
type DockerHandler struct {
	Docker *dockerx.Dockerx
	// Tasks 任务系统（Dockerfile 构建走后台任务）
	Tasks *tasks.Manager

	// statsAll 三件套：docker stats --no-stream 固有 ~2s CPU 采样窗，
	// 列表页 10s 轮询 + 多端并发时每个请求现跑会把资源列拖慢一倍以上。
	// 3s TTL 内直接回上次结果；过期请求持锁重取，并发者在锁上排队、
	// 醒来时缓存已被前一个刷新——等价单飞，不重复打 docker。
	statsMu     sync.Mutex
	statsCached []map[string]interface{}
	statsAt     time.Time
}

// NewDockerHandler 创建 DockerHandler。
func NewDockerHandler(taskMgr *tasks.Manager) *DockerHandler {
	return &DockerHandler{Docker: dockerx.New(), Tasks: taskMgr}
}

// dockerAvailable 探测 docker CLI 可用性，不可用直接 503 + 引导文案。
func (h *DockerHandler) dockerAvailable(c *gin.Context) bool {
	ok, msg := h.Docker.Available()
	if !ok {
		ErrorWithMessage(c, http.StatusServiceUnavailable, "docker 不可用："+msg, nil)
		return false
	}
	return true
}

// safeDockerID 容器 ID/名称白名单：字母数字、下划线、连字符、点。
func safeDockerID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}
