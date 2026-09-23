package tasks

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/apps"
	"github.com/jiuzhao/vmops/service/secretbox"
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

// RegisterAppTasks 注册应用商店与云镜像市场任务 executor。检测走 handler 同步 SSH（快速、用户在线等待），
// 这里注册 app_install；image_download（云镜像市场下载）一并挂入（v3 批次 H）。
//
// masterSecret 为凭据主密钥（与 handler.NewVMCredentialHandler 同源，main.go 注入
// handler.CredentialMasterSecret()——二者必须一致，否则「保存凭据」与「安装取凭据」互相解不开）：
// app_install 要在服务端解密 SSH 凭据，未注入即无法安装（fail-closed，绝不降级成空口令）。
func RegisterAppTasks(m *Manager, masterSecret string) {
	if m == nil {
		return
	}
	m.Register("app_install", func(ctx *ExecContext) error {
		return execAppInstall(ctx, masterSecret)
	})
	RegisterImageDownload(m)
}

// execAppInstall 在虚拟机内经 SSH 执行应用安装脚本（executor 内禁引用 gin/handler）。
// payload：{app_id*, host*, port?, credential_id? | user* + password_enc* + salt*}。
//
// 凭据口径（P0 修复）：payload 随任务入库，绝不再放明文口令 —— 要么放 vm_credentials 的
// 引用 ID（推荐，handler 的 use_saved 通道），要么放 HTTP 边界加密后的密文与盐。
// 明文只在 executor 内存里存在一瞬，用来组装 vmssh.Options；日志与错误文案一律不含口令。
func execAppInstall(ctx *ExecContext, masterSecret string) error {
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
	port := 22
	if p, ok := intParam(payload, "port"); ok && p > 0 && p <= 65535 {
		port = p
	}

	app, found := apps.Get(appID)
	if !found {
		return fmt.Errorf("应用 %s 不在内置应用目录中", appID)
	}

	// 目标白名单复核（纵深防御）：handler 提交时校验过一次，这里按库中 VM 的当前登记地址
	// 再校一次——payload 是持久化数据，历史任务/将来的新提交点都可能带着它进来，
	// 不能因为「提交点校验过」就无条件信任。校验内建于 vmssh.NewOptions，无法绕过。
	user, password, err := resolveSSHCredential(ctx, masterSecret)
	if err != nil {
		return err
	}
	opt, err := vmssh.NewOptions(taskRecordedIP(ctx), host, port, user, password)
	if err != nil {
		return fmt.Errorf("安装目标未通过安全校验: %w", err)
	}

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

// taskRecordedIP 取任务关联虚拟机的登记 IP，作为 SSH 拨号目标白名单的最强约束口径。
// VM 查不到时返回空串（ValidateTarget 随后退回「仅私有网段」口径）并留痕供排查。
func taskRecordedIP(ctx *ExecContext) string {
	recordedIP := ""
	if ctx.Task.VMID != nil && *ctx.Task.VMID > 0 {
		var vm model.VM
		if err := ctx.DB.First(&vm, *ctx.Task.VMID).Error; err != nil {
			// 查不到也不断言放行：退回私有网段校验，并留痕供排查
			log.Printf("[tasks] app_install 复核目标时未找到虚拟机 vm_id=%d err=%v", *ctx.Task.VMID, err)
		} else {
			recordedIP = vm.IP
		}
	}
	return recordedIP
}

// resolveSSHCredential 取安装用 SSH 凭据明文（只在本 executor 内存里流转，不入日志、不回写 DB）。
//
//   - credential_id 通道：按 ID 读 vm_credentials 并解密；必须校验该凭据属于任务关联的 VM，
//     否则会出现「拿 A 机器的口令去连 B 机器地址」的混淆代理人问题（地址虽被白名单收敛，
//     凭据却是别人的，等于跨机复用）；
//   - password_enc + salt 通道：兼容「当场输入口令」的旧前端，handler 已在 HTTP 边界加密。
func resolveSSHCredential(ctx *ExecContext, masterSecret string) (user, password string, err error) {
	if masterSecret == "" {
		return "", "", errors.New("服务端未配置凭据主密钥，无法解析 SSH 凭据")
	}
	if id, ok := intParam(ctx.Payload, "credential_id"); ok && id > 0 {
		var cred model.VMCredential
		if derr := ctx.DB.First(&cred, uint(id)).Error; derr != nil {
			return "", "", fmt.Errorf("读取虚拟机凭据失败: %w", derr)
		}
		if ctx.Task.VMID == nil || cred.VMID != *ctx.Task.VMID {
			return "", "", errors.New("凭据与目标虚拟机不匹配，请重新提交安装任务")
		}
		plain, derr := secretbox.OpenWithMaster(masterSecret, cred.Salt, cred.PasswordEnc)
		if derr != nil {
			return "", "", fmt.Errorf("凭据解密失败: %w", derr)
		}
		return cred.User, string(plain), nil
	}
	user, ok := strParam(ctx.Payload, "user")
	if !ok || user == "" {
		return "", "", errors.New("缺少 SSH 用户名参数")
	}
	cipherB64, ok1 := strParam(ctx.Payload, "password_enc")
	salt, ok2 := strParam(ctx.Payload, "salt")
	if !ok1 || !ok2 || cipherB64 == "" || salt == "" {
		// 明文 password 已不再受理：老任务带着它进来只会被拒，不会拿去拨号
		return "", "", errors.New("缺少 SSH 凭据参数（口令不得以明文传递）")
	}
	plain, derr := secretbox.OpenWithMaster(masterSecret, salt, cipherB64)
	if derr != nil {
		return "", "", fmt.Errorf("SSH 口令解密失败: %w", derr)
	}
	return user, string(plain), nil
}

// taskVMID 取任务已关联的 VM ID（Submit 时由 manager 写入；缺失回 0）。
func taskVMID(ctx *ExecContext) uint {
	if ctx.Task != nil && ctx.Task.VMID != nil {
		return *ctx.Task.VMID
	}
	return 0
}
