package handler

// vm_files_offline_ops.go 离线挂载通道的文件操作端点与路径守卫：目录列举/下载/删除/建目录/上传，
// 以及「挂载点内 + 符号链接不逃逸」的路径清洗（写操作按父目录 realpath 校验，读操作按目标 realpath 校验）。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// offlineWritable 取挂载并要求读写模式（写操作前置守卫）。
func offlineWritable(c *gin.Context, vmID uint) (*offlineMount, bool) {
	offlineMu.Lock()
	m, ok := offlineMounts[vmID]
	offlineMu.Unlock()
	if !ok {
		Fail(c, http.StatusNotFound, "磁盘未挂载（请先执行挂载）")
		return nil, false
	}
	if !m.ReadWrite {
		Fail(c, http.StatusForbidden, "当前为只读挂载；请卸载后改用「读写挂载」再操作")
		return nil, false
	}
	return m, true
}

// offlineSafePath 清洗写操作目标路径：必须在挂载点内，且**父目录**经 realpath 解析后仍在挂载点内。
// 写目标可能尚不存在（上传/建目录），故守卫父目录而非目标本身。
func offlineSafePath(c *gin.Context, m *offlineMount, reqPath string) (string, bool) {
	clean := filepath.Clean(filepath.Join(m.Mountpoint, reqPath))
	if clean == m.Mountpoint {
		Fail(c, http.StatusBadRequest, "不能操作挂载根目录")
		return "", false
	}
	if !strings.HasPrefix(clean, m.Mountpoint+"/") {
		Fail(c, http.StatusBadRequest, "非法路径")
		return "", false
	}
	resolved, err := sudoRun(15*time.Second, "realpath", "-e", filepath.Dir(clean))
	if err != nil {
		Fail(c, http.StatusBadRequest, "父目录不存在")
		return "", false
	}
	resolved = strings.TrimSpace(resolved)
	if resolved != m.Mountpoint && !strings.HasPrefix(resolved, m.Mountpoint+"/") {
		Fail(c, http.StatusBadRequest, "非法路径（符号链接指向挂载点外）")
		return "", false
	}
	return filepath.Join(resolved, filepath.Base(clean)), true
}

// offlineTarget 解析并清洗离线路径：必须在挂载点内（防穿越）。
func offlineTarget(c *gin.Context, vmID uint, reqPath string, needDir bool) (*offlineMount, string, bool) {
	offlineMu.Lock()
	m, ok := offlineMounts[vmID]
	offlineMu.Unlock()
	if !ok {
		Fail(c, http.StatusNotFound, "磁盘未挂载（请先执行挂载）")
		return nil, "", false
	}
	clean := filepath.Clean(filepath.Join(m.Mountpoint, reqPath))
	if clean != m.Mountpoint && !strings.HasPrefix(clean, m.Mountpoint+"/") {
		Fail(c, http.StatusBadRequest, "非法路径")
		return nil, "", false
	}
	// 符号链接守卫：guest 可在磁盘内预置 symlink（绝对目标经宿主内核按宿主根解析，
	// 如 /etc/hostname -> /etc/shadow），上面的词法前缀检查在解析前做，挡不住这种逃逸。
	// 挂载为只读（--ro）、磁盘内容不可变，先解析后访问无 TOCTOU 窗口；
	// realpath -e 逐组件解析全部 symlink，结果必须仍落在挂载点内，否则视为逃逸拒绝。
	resolved, err := sudoRun(15*time.Second, "realpath", "-e", clean)
	if err != nil {
		Fail(c, http.StatusBadRequest, "路径不存在")
		return nil, "", false
	}
	resolved = strings.TrimSpace(resolved)
	if resolved != m.Mountpoint && !strings.HasPrefix(resolved, m.Mountpoint+"/") {
		Fail(c, http.StatusBadRequest, "非法路径（符号链接指向挂载点外）")
		return nil, "", false
	}
	clean = resolved
	if needDir {
		out, err := sudoRun(15*time.Second, "stat", "-c", "%F", clean)
		if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "directory") {
			Fail(c, http.StatusBadRequest, "目录不存在")
			return nil, "", false
		}
	}
	return m, clean, true
}

// OfflineDelete POST /api/vms/:id/files/offline/delete  body {path}（需读写挂载）
func (h *VMFilesHandler) OfflineDelete(c *gin.Context) {
	vm := h.offlineVM(c)
	if vm == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" {
		Fail(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	m, ok := offlineWritable(c, vm.ID)
	if !ok {
		return
	}
	target, ok := offlineSafePath(c, m, req.Path)
	if !ok {
		return
	}
	if _, err := sudoRun(60*time.Second, "rm", "-rf", "--", target); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "删除失败", err)
		return
	}
	Success(c, gin.H{"message": "已删除"})
}

// OfflineMkdir POST /api/vms/:id/files/offline/mkdir  body {path}（需读写挂载）
func (h *VMFilesHandler) OfflineMkdir(c *gin.Context) {
	vm := h.offlineVM(c)
	if vm == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" {
		Fail(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	m, ok := offlineWritable(c, vm.ID)
	if !ok {
		return
	}
	target, ok := offlineSafePath(c, m, req.Path)
	if !ok {
		return
	}
	if _, err := sudoRun(30*time.Second, "mkdir", "-p", "--", target); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "创建目录失败", err)
		return
	}
	Success(c, gin.H{"message": "已创建"})
}

// OfflineUpload POST /api/vms/:id/files/offline/upload（multipart：file + path=目标目录；需读写挂载）
// 经 sudo tee 写入（FUSE 挂载点仅挂载者可见，web 用户写不进去）。
func (h *VMFilesHandler) OfflineUpload(c *gin.Context) {
	vm := h.offlineVM(c)
	if vm == nil {
		return
	}
	dir := c.PostForm("path")
	fh, err := c.FormFile("file")
	if err != nil {
		Fail(c, http.StatusBadRequest, "缺少上传文件")
		return
	}
	m, ok := offlineWritable(c, vm.ID)
	if !ok {
		return
	}
	target, ok := offlineSafePath(c, m, filepath.Join(dir, filepath.Base(fh.Filename)))
	if !ok {
		return
	}
	src, err := fh.Open()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取上传文件失败", err)
		return
	}
	defer src.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "-n", "tee", "--", target)
	cmd.Stdin = src
	cmd.Stdout = io.Discard
	if out, werr := cmd.CombinedOutput(); werr != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "写入失败", fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), werr))
		return
	}
	Success(c, gin.H{"message": "已上传", "path": target})
}

// OfflineList POST /api/vms/:id/files/offline/list  body {path}
func (h *VMFilesHandler) OfflineList(c *gin.Context) {
	vm := h.offlineVM(c)
	if vm == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" {
		Fail(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	m, p, ok := offlineTarget(c, vm.ID, req.Path, true)
	if !ok {
		return
	}
	// FUSE 挂载点仅挂载者可见，读目录经 sudo -n（解析复用 SSH 在线通道的 ls 解析器）
	out, err := sudoRun(30*time.Second, "ls", "-la", "--time-style=+%s", p)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "读取目录失败", err)
		return
	}
	Success(c, gin.H{"path": p, "mountpoint": m.Mountpoint, "items": parseLsOutput(out)})
}

// OfflineDownload GET /api/vms/:id/files/offline/download?path=/etc/hostname
// 直接返回文件内容（octet-stream，非统一信封）。
func (h *VMFilesHandler) OfflineDownload(c *gin.Context) {
	vm := h.offlineVM(c)
	if vm == nil {
		return
	}
	p := c.Query("path")
	if p == "" {
		Fail(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	m, clean, ok := offlineTarget(c, vm.ID, p, false)
	if !ok {
		return
	}
	// 读文件同样经 sudo（FUSE 权限）；空内容校验 sudoRun 区分不出——cat 失败时 err 已带 stderr
	data, err := sudoRun(60*time.Second, "cat", clean)
	if err != nil && data == "" {
		ErrorWithMessage(c, http.StatusBadRequest, "读取文件失败（目录请用浏览）", err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename="+filepath.Base(clean))
	c.Data(http.StatusOK, "application/octet-stream", []byte(data))
	_ = m
}

// listRoot 挂载成功后返回根目录文件名预览。
// guestmount 的 FUSE 挂载默认仅挂载者（root）可见，web 进程直接 ReadDir 会得到空/拒绝，
// 因此离线通道的目录读取统一经 sudo -n。
func listRoot(mp string) []string {
	out, err := sudoRun(30*time.Second, "ls", "-1", mp)
	if err != nil {
		return []string{}
	}
	names := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names
}
