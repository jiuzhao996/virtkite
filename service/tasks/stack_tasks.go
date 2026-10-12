package tasks

// stack_tasks.go 部署栈升级 / 回滚任务（容器深化计划 R5）。
// executor = docker compose pull + up -d：pull 拉新镜像，up -d 只重建镜像发生变化的服务
// （docker compose 自身语义，无需 --force-recreate）。走慢池是因为大栈拉镜像可达数十分钟，
// 同步挂在 HTTP 请求上正是任务管线要解决的问题（app_install 同款取舍）。
//
// 回滚复用同一套部署管线（dockerx.ComposePull + ComposeUpFromFile），只是多一步「把历史版本
// 内容写回部署副本」，不另起一套 compose 调用链——见 execStackRollback。
//
// 约束：executor 运行在 worker goroutine，禁止引用 gin/handler；
// 错误一律 fmt.Errorf("中文描述: %w", err) 保留错误链（完整链由 manager.run 打日志）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/jiuzhao/vmops/service/dockerx"
)

const (
	// stackUpgradeOutTail 成功时写入任务结果的 pull 输出尾部长度（rune）。
	stackUpgradeOutTail = 1000
	// stackUpgradeProgressPull / Up 两段进度（100 留给 manager 的 success 终态统一写）。
	stackUpgradeProgressPull = 40
	stackUpgradeProgressUp   = 90
	// stackTaskTimeout 栈类任务（升级 / 回滚）执行上限：compose pull 大栈可达数十分钟。
	stackTaskTimeout = 30 * time.Minute
)

// RegisterStackTasks 注册栈相关任务 executor，并就地登记分池 / 超时。
//
// stack_rollback 与 stack_upgrade 同为「pull + up」的分钟级任务，必须进慢池并放宽超时，
// 否则会占住快池 worker（仅 4 个）拖慢开关机。分池表（slowTaskTypes）与超时表（taskTimeouts）
// 定义在 manager.go，这里在注册栈任务时一并登记，保持「栈任务相关配置」集中在本文件。
// 调用时机在进程启动、HTTP 服务开始受理之前，此时无并发读者，直接写表安全。
func RegisterStackTasks(m *Manager) {
	if m == nil {
		return
	}
	slowTaskTypes["stack_rollback"] = true
	taskTimeouts["stack_rollback"] = stackTaskTimeout
	m.Register("stack_upgrade", execStackUpgrade)
	m.Register("stack_rollback", execStackRollback)
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

	// 升级成功后把「本次实际生效的编排内容」记入历史（去重：内容没变则不新增）。
	// 快照失败不判任务失败——升级本身已成功，误报 failed 会误导用户；错误留痕 + 结果标记。
	snapErr := snapshotCurrentVersion(ctx, stackID, dir, "升级", nil)
	if snapErr != nil {
		log.Printf("[tasks] 栈升级后记录版本历史失败 stack=%s err=%v", stackID, snapErr)
	}

	setStackResult(ctx, map[string]interface{}{
		"stack_id":         stackID,
		"pull_output":      tailString(out, stackUpgradeOutTail),
		"version_recorded": snapErr == nil,
	})
	return nil
}

// execStackRollback 栈回滚：把指定历史版本的 compose 内容写回部署副本，再复用既有部署管线
// （pull + up -d，与 stack_upgrade 同一套 dockerx 调用）重新起栈。
// payload：{stack_id, dir, version_id}（version_id 为 stack_versions 行 id）。
func execStackRollback(ctx *ExecContext) error {
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
	if filepath.Base(dir) != stackID {
		return errors.New("部署目录与栈 ID 不一致，已拒绝执行")
	}
	versionID, ok := intParam(ctx.Payload, "version_id")
	if !ok || versionID <= 0 {
		return errors.New("缺少回滚目标版本参数")
	}

	// 目标版本必须属于本栈：防「拿 A 栈的历史内容覆盖 B 栈」的跨栈错配
	//（payload 是持久化数据，历史任务 / 将来新提交点都可能带着它进来，纵深防御）。
	target, err := GetStackVersion(ctx.DB, uint(versionID))
	if err != nil {
		return fmt.Errorf("读取回滚目标版本失败: %w", err)
	}
	if target.StackID != stackID {
		return errors.New("回滚目标版本不属于该栈，已拒绝执行")
	}

	// 1) 回滚是破坏性操作：先把「当前生效内容」补记进历史，万一回滚后发现问题还能再滚回来
	//    （「回滚本身也要先进历史」）。fail-closed：这一步失败就不动手——此刻尚未改写任何文件，
	//    直接退出是安全的。去重：若当前内容已是最近一条版本，则不新增。
	if err := snapshotCurrentVersion(ctx, stackID, dir, "回滚前快照", nil); err != nil {
		return fmt.Errorf("记录回滚前版本失败: %w", err)
	}

	// 2) 写回目标版本内容。先写临时文件做 compose config -q 权威校验，通过后再落正式文件，
	//    防 DB 中的脏内容覆盖正在运行的栈（与 handler/stacks.go 的 File 端点同一取舍）。
	reportProgress(ctx, 10, fmt.Sprintf("正在写回 v%d 的编排内容", target.Version))
	tmp := filepath.Join(dir, ".rollback.check.yml")
	if err := os.WriteFile(tmp, []byte(target.Content), 0o644); err != nil {
		return fmt.Errorf("写入校验文件失败: %w", err)
	}
	d := dockerx.New()
	if err := d.ComposeConfigCheck(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("目标版本编排内容校验失败: %w", err)
	}
	_ = os.Remove(tmp)
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(target.Content), 0o644); err != nil {
		return fmt.Errorf("写回编排文件失败: %w", err)
	}

	// 3) 复用既有部署管线：pull + up -d（与 stack_upgrade 完全同路，不另起一套）。
	reportProgress(ctx, stackUpgradeProgressPull, "镜像拉取中")
	out, err := d.ComposePull(dir)
	if err != nil {
		return fmt.Errorf("拉取镜像失败: %w", err)
	}
	reportProgress(ctx, stackUpgradeProgressUp, "镜像已就绪，正在重建服务")
	if err := d.ComposeUpFromFile(dir); err != nil {
		return fmt.Errorf("重建服务失败: %w", err)
	}

	// 4) 回滚后的内容也进历史（RollbackFrom 标记「从 vN 滚回」），审计链可追溯。
	//    失败不判任务失败（回滚本身已成功），仅留痕 + 结果标记。
	from := target.Version
	snapErr := snapshotCurrentVersion(ctx, stackID, dir, fmt.Sprintf("回滚到 v%d", target.Version), &from)
	if snapErr != nil {
		log.Printf("[tasks] 栈回滚后记录版本历史失败 stack=%s err=%v", stackID, snapErr)
	}

	setStackResult(ctx, map[string]interface{}{
		"stack_id":         stackID,
		"rollback_to":      target.Version,
		"pull_output":      tailString(out, stackUpgradeOutTail),
		"version_recorded": snapErr == nil,
	})
	return nil
}

// snapshotCurrentVersion 读取部署目录内 compose 文件内容并记入版本历史（去重）。
// 文件读不到（未部署）或记录失败都返回 error（是否致命由调用方决定）。
func snapshotCurrentVersion(ctx *ExecContext, stackID, dir, summary string, rollbackFrom *int) error {
	raw, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		return fmt.Errorf("读取部署副本失败: %w", err)
	}
	if _, _, err := RecordStackVersion(ctx.DB, stackID, string(raw), summary, ctx.Task.Username, ctx.Task.UserID, rollbackFrom); err != nil {
		return fmt.Errorf("记录版本历史失败: %w", err)
	}
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
