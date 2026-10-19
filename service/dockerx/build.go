// 本文件负责镜像构建：把 docker build 封装成带流式输出回调的同步调用，含标签/上下文路径
// 校验（Dockerfile 词法守卫禁止逃出构建上下文）与超时控制。
package dockerx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

// BuildOpts docker build 参数。
type BuildOpts struct {
	Tag        string            // -t（必填，如 app:1.0）
	Dockerfile string            // -f（相对上下文；空则用上下文根下的 Dockerfile）
	BuildArgs  map[string]string // --build-arg K=V（ARG 构建参数）
	NoCache    bool              // --no-cache
	OnLine     func(line string) // 逐行输出回调（流式构建日志）
	Timeout    time.Duration     // 0 用默认 30 分钟
}

// Build 构建镜像（等价 docker build -t TAG [-f FILE] [--build-arg K=V] CONTEXT）。
// 流式输出经 OnLine 回调（executor 用于实时刷任务结果）；返回合并后的完整输出。
func (d *Dockerx) Build(ctx context.Context, contextDir string, opts BuildOpts) (string, error) {
	opts.Tag = strings.TrimSpace(opts.Tag)
	if err := ValidateTag(opts.Tag); err != nil {
		return "", err
	}
	if opts.Dockerfile != "" {
		// -f 相对上下文：词法守卫禁止逃出上下文目录（否则可读宿主任意文件）
		rel := filepath.Clean(opts.Dockerfile)
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", errors.New("镜像构建路径非法（Dockerfile 须在构建上下文内）")
		}
		opts.Dockerfile = rel
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"build", "-t", opts.Tag}
	if opts.Dockerfile != "" {
		args = append(args, "-f", opts.Dockerfile)
	}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	// --build-arg 顺序稳定（map 遍历无序，排序保证可复现）
	keys := make([]string, 0, len(opts.BuildArgs))
	for k := range opts.BuildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+opts.BuildArgs[k])
	}
	args = append(args, contextDir)

	cmd := exec.CommandContext(cctx, "docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("创建输出管道失败: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("创建错误管道失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动 docker build 失败: %w", err)
	}
	var (
		mu  sync.Mutex
		all []string
		wg  sync.WaitGroup
	)
	scan := func(rc io.Reader) {
		defer wg.Done()
		// 后台 goroutine 的 panic 会带走整个进程；OnLine 是调用方回调，不可信。
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[dockerx] 构建输出读取 panic: %v\n%s", r, debug.Stack())
			}
		}()
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			all = append(all, line)
			mu.Unlock()
			if opts.OnLine != nil {
				opts.OnLine(line)
			}
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	werr := cmd.Wait()
	wg.Wait()
	out := strings.Join(all, "\n")
	if werr != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("docker build 超时: %w", werr)
		}
		if ctx.Err() != nil {
			return out, fmt.Errorf("docker build 已取消: %w", ctx.Err())
		}
		return out, fmt.Errorf("docker build 失败: %w", werr)
	}
	return out, nil
}
