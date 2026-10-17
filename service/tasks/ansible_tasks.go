// ansible_tasks.go：ansible 批量执行任务（P4 自动化运维）——ansible_run 的 executor 实现。
//
// 一次 ansible_run 的生命周期即 execAnsibleRun 内的 1)~5) 编号步骤：
//
//  1. 取目标 VM（只收 vm_id，服务端查库取 IP：裸 IP 无入口，白名单结构性成立）
//  2. 认证通道分流（已注入平台公钥 AnsibleReady 的走免密私钥，其余解密托管口令）
//  3. 生成 run 级 inventory（明文口令只落 0700 瞬时目录，跑完即删）
//  4. 调 ansible 执行（逐行输出经 ctx.Log 实时推 WS、落库节流由 TaskLogger 统一承担）
//  5. 汇总结构化结果（playbook 解析 PLAY RECAP，adhoc 回传原始输出）
//
// 安全口径：口令取自 vm_credentials 密文（secretbox 解密），survey 的 secret 答案经
// 环境变量注入而非 extra-vars/argv，run 目录用完即删；目标缺凭据即整体失败并列出补录
// 清单（fail-closed，不做「有凭据的先跑」的半吊子执行）。
//
// 目标/分组解析见 ansible_targets.go，run 级配置与结果解析见 ansible_runtime.go。
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/ansible"
	"github.com/jiuzhao/vmops/service/secretbox"
)

// ansible 执行常量。
const (
	// ansibleAdhocTimeout adhoc 批量执行兜底超时：局域网内几台 VM 的 ping/command
	// 秒级完成；10 分钟防 SSH 悬挂长期占用 worker。
	ansibleAdhocTimeout = 10 * time.Minute
	// ansiblePlaybookTimeout playbook 兜底超时：装包类（docker/nginx）分钟级，
	// 20 分钟兜底。
	ansiblePlaybookTimeout = 20 * time.Minute
)

// ansiblePlaybookIDRe playbook 文件 id 白名单（与 handler playbookIDRe 同规，executor
// 侧纵深防御复检——payload 是持久化数据，不能因「提交点校验过」就无条件信任）。
var ansiblePlaybookIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// RegisterAnsibleTasks 注册 ansible_run 批量执行 executor（P4 自动化运维）。
//
// masterSecret 与 RegisterAppTasks 同源（main.go 注入 handler.CredentialMasterSecret()）：
// 目标 VM 的 SSH 口令存在 vm_credentials（密文），执行时解密注入 inventory。
func RegisterAnsibleTasks(m *Manager, masterSecret string) {
	if m == nil {
		return
	}
	// 注入主密钥：worker 构建任务日志脱敏器时用它解密本任务相关 VM 的托管凭据
	m.SetMasterSecret(masterSecret)
	m.Register("ansible_run", func(ctx *ExecContext) error {
		return execAnsibleRun(ctx, masterSecret)
	})
}

// execAnsibleRun 批量 adhoc 执行（S1）：payload {targets*[vm_id], module*, args}。
//
// 凭据与目标白名单口径：targets 只收 vm_id，服务端查库取 IP/端口——调用方传裸 IP
// 没有入口（白名单结构性成立，见 service/ansible 包头注释）。口令取自
// vm_credentials（每台一份托管凭据），缺凭据的机器整体 fail 并列出清单（fail-closed，
// 不做「有凭据的先跑」的半吊子执行）。
func execAnsibleRun(ctx *ExecContext, masterSecret string) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}
	// 目标来源：显式 targets（vm id 列表）与可选 group_id（主机组）合并——仍只收 vm_id，
	// 组在服务端解析为成员 id（白名单结构性成立，裸 IP 无入口）；组内成员动态生效。
	targets, _ := uintSliceParam(ctx.Payload, "targets")
	if groupID, ok := intParam(ctx.Payload, "group_id"); ok && groupID > 0 {
		gids, gerr := resolveHostGroupVMIDs(ctx, uint(groupID))
		if gerr != nil {
			return gerr
		}
		targets = mergeUniqueUint(targets, gids)
	}
	if len(targets) == 0 {
		return errors.New("缺少目标虚拟机（targets 或 group_id）")
	}
	playbook, _ := strParam(ctx.Payload, "playbook")
	module, _ := strParam(ctx.Payload, "module")
	moduleArgs, _ := strParam(ctx.Payload, "args")
	isPlaybook := playbook != ""
	if !isPlaybook && module == "" {
		return errors.New("缺少 adhoc 模块（module）或 playbook")
	}

	// 1) 目标 VM：必须存在、运行中、有 IP（无 IP 连 ansible 都够不着）
	var vms []model.VM
	if err := ctx.DB.Where("id IN ?", targets).Find(&vms).Error; err != nil {
		return fmt.Errorf("读取目标虚拟机失败: %w", err)
	}
	if len(vms) != len(targets) {
		return fmt.Errorf("部分目标虚拟机不存在（请求 %d 台，找到 %d 台）", len(targets), len(vms))
	}
	var unreachable []string
	for _, vm := range vms {
		if vm.Status != model.VMStatusRunning || vm.IP == "" {
			unreachable = append(unreachable, vm.Name+"（未运行或无 IP）")
		}
	}
	if len(unreachable) > 0 {
		// 名单放在冒号前：Task.Error 经 friendlyError 取首个冒号前段回显前端
		return fmt.Errorf("%s 不可执行，目标虚拟机需运行中且已获得 IP", strings.Join(unreachable, "、"))
	}

	// 2) 认证通道分流（P4-S4）：平台公钥已注入（AnsibleReady）走私钥免密；其余走
	// 托管口令（每台一份；缺 = fail 并列出补录指引）
	var creds []model.VMCredential
	needPass := []uint{}
	for _, vm := range vms {
		if !vm.AnsibleReady {
			needPass = append(needPass, vm.ID)
		}
	}
	credByVM := map[uint]model.VMCredential{}
	if len(needPass) > 0 {
		if err := ctx.DB.Where("vm_id IN ?", needPass).Find(&creds).Error; err != nil {
			return fmt.Errorf("读取托管凭据失败: %w", err)
		}
		for _, c := range creds {
			credByVM[c.VMID] = c
		}
		var missing []string
		for _, vm := range vms {
			if !vm.AnsibleReady {
				if _, ok := credByVM[vm.ID]; !ok {
					missing = append(missing, vm.Name)
				}
			}
		}
		if len(missing) > 0 {
			// 名单放在冒号前：Task.Error 经 friendlyError 取首个冒号前段回显前端
			return fmt.Errorf("%s 未保存托管凭据（请先到虚拟机详情「凭据」保存，或在自动化页分发平台公钥）", strings.Join(missing, "、"))
		}
		if masterSecret == "" {
			return errors.New("服务端未配置凭据主密钥，无法解密 SSH 凭据")
		}
	}

	// 3) inventory：明文口令只在 run 目录瞬时存在（0700/0600，跑完即删，见包注释）
	reportProgress(ctx, 10, "生成 inventory（"+strconv.Itoa(len(vms))+" 台目标）")
	runDir := config.DataPath("ansible", "runs", strconv.FormatUint(uint64(ctx.Task.ID), 10)+"-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return fmt.Errorf("创建执行目录失败: %w", err)
	}
	defer func() {
		if rerr := os.RemoveAll(runDir); rerr != nil {
			// 目录残留含瞬时口令文件，清理失败必须留痕（人工复核）
			log.Printf("[ansible] 警告: run 目录清理失败 %s: %v", runDir, rerr)
		}
	}()
	hosts := make([]ansible.HostEntry, 0, len(vms))
	nameTaken := map[string]int{}
	keyFile := ""
	if len(needPass) < len(vms) { // 有免密目标才探测私钥
		kf, _, kerr := ansible.EnsureKeyPair(config.DataPath("ansible"))
		if kerr == nil {
			keyFile = kf
		}
	}
	vmHostName := make(map[uint]string, len(vms)) // vm id → inventory 主机名（按组填充分组段）
	for _, vm := range vms {
		name := vm.Name
		nameTaken[name]++
		if nameTaken[name] > 1 {
			// VM 名理论唯一，防御性去重（inventory 主机名必须唯一）
			name = fmt.Sprintf("%s-%d", vm.Name, vm.ID)
		}
		vmHostName[vm.ID] = name
		if vm.AnsibleReady && keyFile != "" {
			// 免密通道也尊重托管凭据里登记的端口（回归：此前固定 22，非 22 端口必失败）
			keyPort := 22
			if c, ok := credByVM[vm.ID]; ok && c.Port > 0 {
				keyPort = c.Port
			}
			hosts = append(hosts, ansible.HostEntry{Name: name, IP: vm.IP, User: ansibleSSHUser(vm), Port: keyPort, KeyFile: keyFile})
			continue
		}
		cred := credByVM[vm.ID]
		port := cred.Port
		if port <= 0 {
			port = 22
		}
		plain, derr := secretbox.OpenWithMaster(masterSecret, cred.Salt, cred.PasswordEnc)
		if derr != nil {
			return fmt.Errorf("解密 %s 的托管凭据失败: %w", vm.Name, derr)
		}
		hosts = append(hosts, ansible.HostEntry{Name: name, IP: vm.IP, User: cred.User, Port: port, Pass: string(plain)})
	}
	eng, err := ansible.Detect()
	if err != nil {
		return fmt.Errorf("ansible 引擎不可用: %w", err)
	}
	inv, err := ansible.BuildInventory(runDir, hosts, buildAnsibleGroups(ctx, vms, vmHostName))
	if err != nil {
		return err
	}

	// 4) 执行：组装运行参数。逐行回调写结构化日志（前端经日志 WS 即见实时输出；
	//    落库节流由 TaskLogger 统一承担，不再在这里自建 2s 攒批刷 result）。
	var runOpts ansible.RunOpts
	if isPlaybook {
		// playbook 分支：id 在 handler 轻校验过，这里按白名单正则后再拼路径（纵深防御）
		if !ansiblePlaybookIDRe.MatchString(playbook) {
			return errors.New("playbook ID 非法")
		}
		// playbook 路径解析：<id>.yml 单文件 或 <id>/ 目录（role 形态，主文件 site.yml）
		pbPath, _, ok := ansible.ResolvePlaybook(config.DataPath("ansible", "playbooks"), playbook)
		if !ok {
			return fmt.Errorf("playbook 不存在: %s", playbook)
		}
		runOpts = ansible.RunOpts{Inventory: inv, Playbook: pbPath, Timeout: ansiblePlaybookTimeout}
		reportProgress(ctx, 20, "ansible-playbook "+playbook+" 执行中（"+strconv.Itoa(len(hosts))+" 台）")
	} else {
		runOpts = ansible.RunOpts{Inventory: inv, Module: module, ModuleArgs: moduleArgs, Timeout: ansibleAdhocTimeout}
		reportProgress(ctx, 20, "ansible "+module+" 执行中（"+strconv.Itoa(len(hosts))+" 台）")
	}
	// 平台托管的 run 级 ansible.cfg（forks / facts 缓存 / interpreter），随 run 目录删除
	runOpts.ConfigFile = writeRunAnsibleConfig(runDir)
	// 预演 / 差异 / 标签（仅 playbook 语义，executor 不额外拦截——ansible 自己忽略无关 flag）
	runOpts.Check, _ = boolParam(ctx.Payload, "check")
	runOpts.Diff, _ = boolParam(ctx.Payload, "diff")
	runOpts.Tags, _ = strParam(ctx.Payload, "tags")
	runOpts.SkipTags, _ = strParam(ctx.Payload, "skip_tags")
	// survey 执行参数分流（安全要点）：定义从 playbook 头部解析，答案按类型走不同通道——
	//   - 非 secret → 写成 YAML 文件走 --extra-vars @file（字符串打 !unsafe 防 Jinja 注入）
	//   - secret    → payload 里存的是密文，此处解密后经环境变量注入（不进 extra-vars）
	// 命令行只出现文件路径，用户填的值永不进 argv。无 survey 定义时按旧 vars= 语义全量落盘。
	if ef, ev, aerr := ansible.ApplySurveyAnswers(runOpts.Playbook, runDir, ctx.Payload, masterSecret); aerr != nil {
		return aerr
	} else {
		if ef != "" {
			runOpts.ExtraVarsFile = ef
		}
		if len(ev) > 0 {
			runOpts.EnvVars = ev
		}
	}

	// 内存 buf 仅用于拼装终态业务结果里的 output 字段（保持既有结果结构不变）。
	// 实时日志走 ctx.Log（结构化条目 + 落库节流 + WS 推送），这里不再做中间 result 刷库。
	var (
		mu  sync.Mutex
		buf []string
	)
	runOpts.OnLine = func(line string) {
		mu.Lock()
		buf = append(buf, line)
		mu.Unlock()
		if ctx.Log != nil {
			ctx.Log.AppendLine(line)
		}
	}
	if ctx.Log != nil {
		if isPlaybook {
			ctx.Log.Command("ansible-playbook " + playbook)
		} else {
			ctx.Log.Command("ansible -m " + module + " " + moduleArgs)
		}
	}
	// AU2 取消：ctx 来自 Manager（用户取消时子进程被 kill，Run 返回 ctx.Canceled）
	runCtx := ctx.Ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	recap, err := eng.Run(runCtx, runOpts)

	// 5) 结构化结果：无论成败都回传 output/RECAP。
	// 失败时 manager 也会把 ctx.Task.Result 落库，自动化页才能定位是哪台主机失败；
	// 否则 friendlyError 在第一个冒号处截断，只剩「部分主机执行失败」，RECAP 全丢。
	mu.Lock()
	output := truncate(strings.Join(buf, "\n"), 50_000)
	mu.Unlock()
	reportProgress(ctx, 90, "汇总执行结果")
	result := map[string]interface{}{
		"module":    module,
		"playbook":  playbook,
		"targets":   len(hosts),
		"output":    output,
		"recap":     parseRecap(recap),
		"recap_raw": recap,
		"inventory": sanitizeInventory(inv),
	}
	raw, _ := json.Marshal(result)
	ctx.Task.Result = string(raw)

	if err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}
