package tasks

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/service/apps"
	"github.com/jiuzhao/vmops/service/vmssh"
)

// app_install 任务超时与截断常量。
const (
	// appDetectTimeout 已装检测超时：Detect 都是 command -v/docker ps 级别的轻量命令，
	// 15s 主要覆盖 SSH 建连与认证耗时。
	appDetectTimeout = 15 * time.Second
	// appInstallTimeout 安装脚本超时：apt 拉包安装（nginx/mysql/docker）为分钟级，
	// 15 分钟兜底，防止 SSH 会话悬挂长期占用 worker。
	appInstallTimeout = 15 * time.Minute
	// appErrTail 失败时写入错误链的 stderr 尾部长度（rune）。
	appErrTail = 500
	// appOutTail 成功时写入任务结果的 stdout 尾部长度（rune）。
	appOutTail = 1000
)

// 已装/未装判定标记：检测结果用 stdout 标记区分而非退出码——vmssh.Run 契约只回
// (stdout, stderr, err)，「命令退出码 1=未安装」与「SSH 连接/认证失败」无法靠退出码
// 分开；把 Detect 包一层 if/else 后，脚本恒定退出码 0，退出码非 0 就只剩连接层失败
// 一种含义，两类失败彻底分开。
const (
	appInstalledMark    = "__VMOPS_INSTALLED__"
	appNotInstalledMark = "__VMOPS_NOT_INSTALLED__"
)

// buildDetectCmd 把单行检测命令包成恒定退出码 0 的判别脚本（纯函数，供单测）。
// Detect 必须单行（service/apps 的目录约定有单测保障）。
func buildDetectCmd(detect string) string {
	return "if " + detect + "; then echo " + appInstalledMark + "; else echo " + appNotInstalledMark + "; fi"
}

// RegisterAppTasks 注册应用商店任务 executor。检测走 handler 同步 SSH（快速、用户在线等待），
// 这里只注册 app_install。
func RegisterAppTasks(m *Manager) {
	if m == nil {
		return
	}
	m.Register("app_install", execAppInstall)
}

// execAppInstall 在虚拟机内经 SSH 执行应用安装脚本（executor 内禁引用 gin/handler）。
// payload：{app_id*, host*, port?, user*, password*}。
// 凭据只进 payload（随任务入库，与 create_vm 的 cloud_init 密码同一口径），
// 日志与错误文案一律不含 password。
func execAppInstall(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}
	payload := ctx.Payload

	appID, ok := strParam(payload, "app_id")
	if !ok || appID == "" {
		return errors.New("缺少应用 ID 参数")
	}
	host, ok := strParam(payload, "host")
	if !ok || host == "" {
		return errors.New("缺少目标主机地址参数")
	}
	user, ok := strParam(payload, "user")
	if !ok || user == "" {
		return errors.New("缺少 SSH 用户名参数")
	}
	password, ok := strParam(payload, "password")
	if !ok || password == "" {
		return errors.New("缺少 SSH 密码参数")
	}
	port := 22
	if p, ok := intParam(payload, "port"); ok && p > 0 && p <= 65535 {
		port = p
	}

	app, found := apps.Get(appID)
	if !found {
		return fmt.Errorf("应用 %s 不在内置应用目录中", appID)
	}

	opt := vmssh.Options{Host: host, Port: port, User: user, Password: password}

	// 1) 已装检测：连接失败（err）与未安装（NOT_INSTALLED 标记）两类结果彻底分开。
	reportProgress(ctx, 5, "正在检测 "+app.Name+" 是否已安装")
	stdout, _, err := vmssh.Run(opt, buildDetectCmd(app.Detect), appDetectTimeout)
	if err != nil {
		// 连接/认证失败：完整错误链经 run() 进服务端日志，任务错误只留中文前缀
		return fmt.Errorf("连接虚拟机执行检测失败: %w", err)
	}
	switch {
	case strings.Contains(stdout, appInstalledMark):
		reportProgress(ctx, 100, app.Name+" 已安装，跳过")
		setTaskResultVM(ctx, map[string]interface{}{
			"app_id":  app.ID,
			"skipped": true,
		}, taskVMID(ctx), ctx.Task.VMName)
		return nil
	case strings.Contains(stdout, appNotInstalledMark):
		// 未安装，继续安装
	default:
		return errors.New("检测结果无法识别，虚拟机 shell 输出异常")
	}

	// 2) 执行安装脚本（15 分钟兜底超时）。
	reportProgress(ctx, 10, "开始安装 "+app.Name+"（apt 安装耗时约 1-5 分钟）")
	installOut, installErrOut, err := vmssh.Run(opt, app.Install, appInstallTimeout)
	if err != nil {
		// stderr 尾部 500 字并入错误链：中文前缀给前端（friendlyError 取首个冒号前段），
		// stderr 细节与完整 err 链进服务端日志
		return fmt.Errorf("安装 %s 失败: %s: %w", app.Name, truncate(installErrOut, appErrTail), err)
	}

	reportProgress(ctx, 100, app.Name+" 安装完成")
	setTaskResultVM(ctx, map[string]interface{}{
		"app_id": app.ID,
		"output": truncate(installOut, appOutTail),
	}, taskVMID(ctx), ctx.Task.VMName)
	return nil
}

// taskVMID 取任务已关联的 VM ID（Submit 时由 manager 写入；缺失回 0）。
func taskVMID(ctx *ExecContext) uint {
	if ctx.Task != nil && ctx.Task.VMID != nil {
		return *ctx.Task.VMID
	}
	return 0
}
