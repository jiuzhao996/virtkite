// image_build.go：Dockerfile 构建 executor（docker build）——平台从「只能拉镜像」补到「能造镜像」。
//
// payload：{context*, tag*, dockerfile?, build_args?{K:V}, no_cache?}。
// 构建日志流式回写任务结果（前端轮询任务即见实时构建输出）。
// 约束：executor 运行在 worker goroutine，禁止引用 gin/handler。
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dockerbuild"
	"github.com/jiuzhao/vmops/service/dockerx"
)

// imageBuildFlushInterval 构建日志节流落库间隔（每行一次 UPDATE 会打爆 DB）。
const imageBuildFlushInterval = 2 * time.Second

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

	// 日志节流落库：攒行 + 2s 刷一次（与 ansible_run 同款取舍）。
	// 必须加锁：dockerx.Build 对 stdout/stderr 各起一个 goroutine 读管道，OnLine 是并发调用的，
	// 裸 slice 追加会数据竞争（-race 可复现），严重时 slice 头被写坏。
	var (
		mu   sync.Mutex
		buf  []string
		last = time.Now()
	)
	flush := func() {
		text := truncate(strings.Join(buf, "\n"), 60_000)
		if err := ctx.DB.Model(&model.Task{}).Where("id = ?", ctx.Task.ID).Update("result", text).Error; err != nil {
			// 中间刷新失败不致命（终态由 worker 收口），留痕即可
			log.Printf("[image_build] 任务 %d 中间日志落库失败: %v", ctx.Task.ID, err)
		}
		ctx.Task.Result = text
	}
	onLine := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		buf = append(buf, line)
		if time.Since(last) < imageBuildFlushInterval {
			return
		}
		last = time.Now()
		flush()
	}

	out, err := dockerx.New().Build(base, dir, dockerx.BuildOpts{
		Tag:        strings.TrimSpace(tag),
		Dockerfile: strings.TrimSpace(dockerfile),
		BuildArgs:  buildArgs,
		NoCache:    noCache,
		OnLine:     onLine,
	})
	// 收尾刷一次：最后不足一个节流窗口的日志不能丢
	mu.Lock()
	flush()
	mu.Unlock()
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
