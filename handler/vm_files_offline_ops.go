package handler

// vm_files_offline_ops.go 离线挂载通道的文件操作端点与路径守卫：目录列举/下载/删除/建目录/上传，
// 以及「挂载点内 + 符号链接不逃逸」的路径清洗（写操作按父目录 realpath 校验，读操作按目标 realpath 校验）。

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// offlineWritable 取挂载并要求读写模式（写操作前置守卫）。
//
// 只在锁内读取 map 中的指针，随后释放锁：offlineMount 内容在挂载生命周期内
// 不变（卸载由独立流程处理），无需长期持锁。只读挂载直接 403 而不是静默失败，
// 是为了让前端明确提示「卸载后改用读写挂载」。
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
//
// 两道防线：
//  1. 词法检查：Clean 后必须仍以 mountpoint+"/" 开头，挡住 ../ 穿越与挂载根本身；
//  2. 真实路径检查：对父目录跑 realpath -e，挡住 guest 预置的符号链接（绝对链接
//     会被宿主内核按宿主根解析，词法前缀看不出来）。
//
// 返回的是「解析后的父目录 + 原始基名」，后续 rm/mkdir/tee 都在真实目录下操作。
// 注意调用方一律用 `--` 分隔选项，避免以 - 开头的文件名被当成命令参数。
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
// rm -rf 是递归删除：路径已由 offlineSafePath 收敛到挂载点内的真实父目录，
// 且用 `--` 终止选项解析，防止用户提交 "-rf" 之类的文件名造成参数注入。
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
// mkdir -p 允许一次创建多级目录；目标父目录已通过 offlineSafePath 校验。
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
//
// 目标文件名只取上传文件的 Base，丢弃客户端可能带上的目录部分；path 视为
// 「目标目录」，与文件名拼接后再过 offlineSafePath。整个写入包在 5 分钟超时
// 的 context 里，避免大文件/异常流把请求永久挂住。
//
// 注意（既有实现，本注释批次不改行为）：下方先把 cmd.Stdout 设为 io.Discard，
// 随后调用 CombinedOutput；Go 标准库规定 CombinedOutput 要求 Stdout/Stderr
// 均未设置，否则直接返回 "exec: Stdout already set"，命令不会执行。因此该
// 上传路径实际会稳定失败，需后续单独修复（保留 stdin 流式写入 + 捕获 stderr
// 的正确做法是改用 cmd.Output()/自定义 buffer，而不是 CombinedOutput）。
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
	// tee 会把内容回显到 stdout；上传可能是大文件，stdout 必须丢弃（否则 CombinedOutput 会把
	// 整份内容读进内存）。但 CombinedOutput 在 Stdout 已设置时会直接返回 "exec: Stdout already
	// set" 且**不执行命令**——所以这里用 Run()，stderr 单独捕获用于错误信息。
	cmd.Stdout = io.Discard
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if werr := cmd.Run(); werr != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "写入失败", fmt.Errorf("%s: %w", strings.TrimSpace(stderr.String()), werr))
		return
	}
	Success(c, gin.H{"message": "已上传", "path": target})
}

// OfflineList POST /api/vms/:id/files/offline/list  body {path}
// needDir=true 时先确认目标确实是目录（stat），再交给 ls；返回的 path 是
// realpath 解析后的真实路径，前端后续操作应以它为准。
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
//
// 这是本文件里唯一不走 {code,message,data} 的端点：浏览器下载需要原始字节流
// 与 Content-Disposition，包一层 JSON 反而要前端再解码。needDir=false 允许
// 目标是任意文件；sudoRun 在 cat 成功但内容为空时也会返回 nil，故只有
// 「err != nil 且 data == ""」才判定失败（空文件是合法结果）。
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
	// 前置 stat：既确认文件可读（不存在/无权限在这里就能给出 400），也拿到大小写入
	// Content-Length。目录会在 stat 后仍被 cat 拒绝，由 stderr 留痕。
	out, serr := sudoRun(30*time.Second, "stat", "-c", "%s", clean)
	if serr != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "读取文件失败（目录请用浏览）", serr)
		return
	}
	size, perr := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if perr != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "读取文件失败（目录请用浏览）",
			fmt.Errorf("stat 输出无法解析: %q", out))
		return
	}
	// 流式输出：离线通道正是为「大文件 / 无法 SSH」准备的，不能像 SSH 通道那样
	// 全量缓冲进内存再 []byte(...) 复制一次（数 GB 文件会直接把进程堆打爆）。
	// 客户端断开时请求 context 取消，CommandContext 会终止 sudo/cat。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "-n", "cat", clean)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取文件失败", err)
		return
	}
	if err := cmd.Start(); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取文件失败", err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename="+filepath.Base(clean))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Status(http.StatusOK)
	if _, cerr := io.Copy(c.Writer, stdout); cerr != nil {
		log.Printf("[vm-files] 离线下载中断 vm=%d path=%s err=%v", vm.ID, clean, cerr)
	}
	if werr := cmd.Wait(); werr != nil {
		log.Printf("[vm-files] 离线下载 cat 失败 vm=%d path=%s err=%v stderr=%s",
			vm.ID, clean, werr, strings.TrimSpace(stderr.String()))
	}
	_ = m // m 仅用于确认挂载存在；下载只依赖已清洗的真实路径 clean
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
