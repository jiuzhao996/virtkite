// docker_build.go：Dockerfile 构建入口——构建上下文管理（data/docker-builds/<name>/）
// 与提交构建任务（image_build 异步任务，日志流式回写任务结果）。
//
// 平台此前只能拉镜像不能造镜像；本文件补上「写 Dockerfile → 构建 → 得到本地镜像」这条主线。
package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/dockerbuild"
	"github.com/jiuzhao/vmops/service/dockerx"
)

// ListBuildContexts GET /api/docker/builds —— 构建上下文列表。
func (h *DockerHandler) ListBuildContexts(c *gin.Context) {
	items, err := dockerbuild.List()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取构建上下文失败", err)
		return
	}
	Success(c, gin.H{"items": items})
}

// CreateBuildContext POST /api/docker/builds body {name}
func (h *DockerHandler) CreateBuildContext(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if err := dockerbuild.Create(strings.TrimSpace(req.Name)); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已创建构建上下文"})
}

// DeleteBuildContext DELETE /api/docker/builds/:name
func (h *DockerHandler) DeleteBuildContext(c *gin.Context) {
	if err := dockerbuild.Remove(c.Param("name")); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已删除构建上下文"})
}

// ListBuildFiles GET /api/docker/builds/:name/files?path=.
func (h *DockerHandler) ListBuildFiles(c *gin.Context) {
	rel := c.DefaultQuery("path", ".")
	files, err := dockerbuild.ListFiles(c.Param("name"), rel)
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"items": files, "path": rel})
}

// ReadBuildFile GET /api/docker/builds/:name/file?path=Dockerfile
func (h *DockerHandler) ReadBuildFile(c *gin.Context) {
	content, err := dockerbuild.ReadFile(c.Param("name"), c.Query("path"))
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"content": content})
}

// WriteBuildFile PUT /api/docker/builds/:name/file body {path, content}
func (h *DockerHandler) WriteBuildFile(c *gin.Context) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		Fail(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	if err := dockerbuild.WriteFile(c.Param("name"), req.Path, req.Content); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已保存"})
}

// UploadBuildFile POST /api/docker/builds/:name/upload（multipart：file + path=相对路径）
// 用于 jar/压缩包等二进制上下文文件（文本文件走 PUT /file）。
func (h *DockerHandler) UploadBuildFile(c *gin.Context) {
	name := c.Param("name")
	rel := strings.TrimSpace(c.PostForm("path"))
	fh, err := c.FormFile("file")
	if err != nil {
		Fail(c, http.StatusBadRequest, "缺少上传文件")
		return
	}
	if rel == "" {
		rel = fh.Filename
	}
	src, err := fh.Open()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取上传文件失败", err)
		return
	}
	defer src.Close()
	n, err := dockerbuild.SaveUpload(name, rel, src)
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已上传", "size": n})
}

// DeleteBuildFile DELETE /api/docker/builds/:name/file?path=
func (h *DockerHandler) DeleteBuildFile(c *gin.Context) {
	if err := dockerbuild.DeleteFile(c.Param("name"), c.Query("path")); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已删除"})
}

// BuildImage POST /api/docker/builds/:name/build body {tag*, dockerfile?, build_args?, no_cache?}
// —— 提交 image_build 异步任务（202 + task_id），构建日志经任务结果流式回显。
func (h *DockerHandler) BuildImage(c *gin.Context) {
	if !h.dockerAvailable(c) {
		return
	}
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	name := c.Param("name")
	if _, err := dockerbuild.ContextDir(name); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		Tag        string            `json:"tag"`
		Dockerfile string            `json:"dockerfile"`
		BuildArgs  map[string]string `json:"build_args"`
		NoCache    bool              `json:"no_cache"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Tag) == "" {
		Fail(c, http.StatusBadRequest, "缺少镜像标签（tag）")
		return
	}
	if err := dockerx.ValidateTag(strings.TrimSpace(req.Tag)); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	payload := map[string]interface{}{
		"context": name,
		"tag":     strings.TrimSpace(req.Tag),
	}
	if strings.TrimSpace(req.Dockerfile) != "" {
		payload["dockerfile"] = strings.TrimSpace(req.Dockerfile)
	}
	if len(req.BuildArgs) > 0 {
		payload["build_args"] = req.BuildArgs
	}
	if req.NoCache {
		payload["no_cache"] = true
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("image_build", "构建镜像 "+req.Tag, payload, userID, username, "", nil)
	if err != nil {
		SubmitTaskError(c, err)
		return
	}
	Accepted(c, "构建任务已提交", gin.H{"task_id": task.ID})
}
