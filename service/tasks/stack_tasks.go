package tasks

// stack_tasks.go 部署栈升级任务（容器深化计划 R5）。
// executor = docker compose pull + up -d：pull 拉新镜像，up -d 只重建镜像发生变化的服务
// （docker compose 自身语义，无需 --force-recreate）。走慢池是因为大栈拉镜像可达数十分钟，
// 同步挂在 HTTP 请求上正是任务管线要解决的问题（app_install 同款取舍）。
//
// 约束：executor 运行在 worker goroutine，禁止引用 gin/handler；
// 错误一律 fmt.Errorf("中文描述: %w", err) 保留错误链（完整链由 manager.run 打日志）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jiuzhao/vmops/service/dockerx"
)

const (
	// stackUpgradeOutTail 成功时写入任务结果的 pull 输出尾部长度（rune）。
	stackUpgradeOutTail = 1000
	// stackUpgradeProgressPull / Up 两段进度（100 留给 manager 的 success 终态统一写）。
	stackUpgradeProgressPull = 40
	stackUpgradeProgressUp   = 90
)

// RegisterStackTasks 注册栈相关任务 executor。
func RegisterStackTasks(m *Manager) {
	if m == nil {
		return
	}
	m.Register("stack_upgrade", execStackUpgrade)
}

// execStackUpgrade 栈升级：先 pull 全部镜像，再 up -d 重建变化的服务。
// payload：{stack_id, dir}（dir 为 data/stacks/<id> 部署目录，compose 文件在其内）。
func execStackUpgrade(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}
	stackID, ok := strParam(ctx.Payload, "stack_id")
	if !ok || stackID == "" {
		return errors.New("缺少栈 ID 参数")
	}
	dir, ok := strParam(ctx.Payload, "dir")
	if !ok || dir == "" {
		return errors.New("缺少部署目录参数")
	}
	// 目录名必须与栈 ID 一致：compose 项目名=目录名，栈↔编排 tab 的闭环依赖此约束
	if filepath.Base(dir) != stackID {
		return errors.New("部署目录与栈 ID 不一致，已拒绝执行")
	}

	d := dockerx.New()

	reportProgress(ctx, 5, "正在拉取镜像（大栈可能需要较长时间）")
	reportProgress(ctx, stackUpgradeProgressPull, "镜像拉取中")
	out, err := d.ComposePull(dir)
	if err != nil {
		return fmt.Errorf("拉取镜像失败: %w", err)
	}

	reportProgress(ctx, stackUpgradeProgressUp, "镜像已就绪，正在重建服务")
	if err := d.ComposeUpFromFile(dir); err != nil {
		return fmt.Errorf("重建服务失败: %w", err)
	}

	setStackResult(ctx, map[string]interface{}{
		"stack_id":    stackID,
		"pull_output": tailString(out, stackUpgradeOutTail),
	})
	return nil
}

// setStackResult 回填栈类任务结果（无 VM 关联，不动 VMID/VMName——栈不是某台虚拟机的资源）。
func setStackResult(ctx *ExecContext, result map[string]interface{}) {
	if b, err := json.Marshal(result); err == nil {
		ctx.Task.Result = string(b)
	}
}

// tailString 取字符串尾部 n 个 rune（超长输出只留末尾，防撑爆任务结果列）。
func tailString(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
