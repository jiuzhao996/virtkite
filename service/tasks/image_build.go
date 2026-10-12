// image_build.go：Dockerfile 构建 executor（docker build）——平台从「只能拉镜像」补到「能造镜像」。
//
// payload：{context*, tag*, dockerfile?, build_args?{K:V}, no_cache?}。
// 构建日志实时写入结构化任务日志（ctx.Log → tasks.log_entries + 日志 WS 推送）。
// 约束：executor 运行在 worker goroutine，禁止引用 gin/handler。
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jiuzhao/vmops/service/dockerbuild"
	"github.com/jiuzhao/vmops/service/dockerx"
)

// RegisterImageBuild 注册 Dockerfile 构建 executor。
func RegisterImageBuild(m *Manager) {
	if m == nil {
		return
	}
	m.Register("image_build", execImageBuild)
}

func execImageBuild(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}
	name, _ := strParam(ctx.Payload, "context")
	tag, _ := strParam(ctx.Payload, "tag")
	dockerfile, _ := strParam(ctx.Payload, "dockerfile")
	noCache, _ := boolParam(ctx.Payload, "no_cache")
	if strings.TrimSpace(tag) == "" {
		return errors.New("缺少镜像标签")
	}
	dir, err := dockerbuild.ContextDir(name)
	if err != nil {
		return err
	}
	buildArgs := map[string]string{}
	if raw, ok := ctx.Payload["build_args"].(map[string]interface{}); ok {
		for k, v := range raw {
			buildArgs[k] = fmt.Sprintf("%v", v)
		}
	}

	base := ctx.Ctx
	if base == nil {
		base = context.Background()
	}
	reportProgress(ctx, 5, "开始构建镜像 "+tag)

	// 构建日志实时写入结构化日志器：落库节流（2s 攒批）与 WS 推送由 TaskLogger 统一承担，
	// 不再在这里自建 buf/节流刷 result。AppendLine 会对 docker pull/build 的进度噪音行
	// 自动折叠（hidden=true），前端默认收起、可展开。
	// 注意：dockerx.Build 对 stdout/stderr 各起一个读协程，onLine 会被并发调用——
	// TaskLogger.append 内部持锁，天然并发安全。
	onLine := func(line string) {
		if ctx.Log != nil {
			ctx.Log.AppendLine(line)
		}
	}
	if ctx.Log != nil {
		ctx.Log.Command("docker build -t " + strings.TrimSpace(tag))
	}

	out, err := dockerx.New().Build(base, dir, dockerx.BuildOpts{
		Tag:        strings.TrimSpace(tag),
		Dockerfile: strings.TrimSpace(dockerfile),
		BuildArgs:  buildArgs,
		NoCache:    noCache,
		OnLine:     onLine,
	})
	if err != nil {
		return fmt.Errorf("镜像构建失败: %w", err)
	}
	reportProgress(ctx, 95, "构建完成")
	result := map[string]interface{}{
		"tag":     strings.TrimSpace(tag),
		"context": name,
		"output":  truncate(out, 20_000),
	}
	raw, _ := json.Marshal(result)
	ctx.Task.Result = string(raw)
	return nil
}
