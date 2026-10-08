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
	// ansibleFlushInterval 输出节流落库间隔：ansible 逐行输出可能很快，
	// 每行一次 UPDATE 会打爆 DB，按间隔攒批。
	ansibleFlushInterval = 2 * time.Second
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
			user := "root"
			if cred, ok := credByVM[vm.ID]; ok && cred.User != "" {
				user = cred.User
			}
			hosts = append(hosts, ansible.HostEntry{Name: name, IP: vm.IP, User: user, Port: 22, KeyFile: keyFile})
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

	// 4) 执行：逐行回调攒输出，节流刷任务 Result（前端轮询任务即见实时日志）
	var runOpts ansible.RunOpts
	if isPlaybook {
		// playbook 分支：id 在 handler 轻校验过，这里按白名单正则后再拼路径（纵深防御）
		if !ansiblePlaybookIDRe.MatchString(playbook) {
			return errors.New("playbook ID 非法")
		}
		pbPath := config.DataPath("ansible", "playbooks", playbook+".yml")
		if _, perr := os.Stat(pbPath); perr != nil {
			return fmt.Errorf("playbook 不存在: %s", playbook)
		}
		runOpts = ansible.RunOpts{Inventory: inv, Playbook: pbPath, Timeout: ansiblePlaybookTimeout}
		reportProgress(ctx, 20, "ansible-playbook "+playbook+" 执行中（"+strconv.Itoa(len(hosts))+" 台）")
	} else {
		runOpts = ansible.RunOpts{Inventory: inv, Module: module, ModuleArgs: moduleArgs, Timeout: ansibleAdhocTimeout}
		reportProgress(ctx, 20, "ansible "+module+" 执行中（"+strconv.Itoa(len(hosts))+" 台）")
	}
	if raw, ok := ctx.Payload["extra_vars"].(map[string]interface{}); ok && len(raw) > 0 {
		ev := map[string]string{}
		for k, v := range raw {
			ev[k] = fmt.Sprintf("%v", v)
		}
		runOpts.ExtraVars = ev
	}
	var (
		mu    sync.Mutex
		buf   []string
		dirty bool
	)
	flush := func() {
		mu.Lock()
		defer mu.Unlock()
		if !dirty {
			return
		}
		text := truncate(strings.Join(buf, "\n"), 60_000)
		if err := ctx.DB.Model(&model.Task{}).Where("id = ?", ctx.Task.ID).Update("result", text).Error; err != nil {
			// 中间刷新失败不致命（终态由 worker 收口），留痕即可
			log.Printf("[ansible] 任务 %d 中间结果落库失败: %v", ctx.Task.ID, err)
		}
		ctx.Task.Result = text
		dirty = false
	}
	stopFlusher := make(chan struct{})
	go func() {
		ticker := time.NewTicker(ansibleFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				flush()
			case <-stopFlusher:
				return
			}
		}
	}()
	runOpts.OnLine = func(line string) {
		mu.Lock()
		buf = append(buf, line)
		dirty = true
		mu.Unlock()
	}
	// AU2 取消：ctx 来自 Manager（用户取消时子进程被 kill，Run 返回 ctx.Canceled）
	runCtx := ctx.Ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	recap, err := eng.Run(runCtx, runOpts)
	close(stopFlusher)
	flush()
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	// 5) 结构化结果：playbook 有 PLAY RECAP 逐主机计数；adhoc 输出原文随终态回传
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
	}
	raw, _ := json.Marshal(result)
	ctx.Task.Result = string(raw)
	return nil
}

// resolveHostGroupVMIDs 解析主机组成员为 VM id 列表（VMIDs 是 JSON 文本数组）。
func resolveHostGroupVMIDs(ctx *ExecContext, groupID uint) ([]uint, error) {
	var g model.HostGroup
	if err := ctx.DB.First(&g, groupID).Error; err != nil {
		return nil, fmt.Errorf("主机组不存在: %w", err)
	}
	if strings.TrimSpace(g.VMIDs) == "" {
		return nil, nil
	}
	var ids []uint
	if err := json.Unmarshal([]byte(g.VMIDs), &ids); err != nil {
		return nil, fmt.Errorf("主机组「%s」成员数据损坏: %w", g.Name, err)
	}
	return ids, nil
}

// mergeUniqueUint 合并两个 id 切片并去重（保持 a 在前、b 在后的稳定顺序）。
func mergeUniqueUint(a, b []uint) []uint {
	seen := make(map[uint]bool, len(a)+len(b))
	out := make([]uint, 0, len(a)+len(b))
	for _, x := range a {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	for _, x := range b {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// buildAnsibleGroups 生成 inventory 分组段（组名 → 主机名列表）：把本次目标 VM 按其所属
// 主机组归类，供 playbook 用 `hosts: <组名>` 指定目标。只含本次目标里的成员（BuildInventory
// 另有防御性过滤），坏组跳过不阻断。
func buildAnsibleGroups(ctx *ExecContext, vms []model.VM, nameByVM map[uint]string) map[string][]string {
	var groups []model.HostGroup
	if err := ctx.DB.Find(&groups).Error; err != nil || len(groups) == 0 {
		return nil
	}
	targetSet := make(map[uint]bool, len(vms))
	for _, vm := range vms {
		targetSet[vm.ID] = true
	}
	out := make(map[string][]string)
	for _, g := range groups {
		if strings.TrimSpace(g.VMIDs) == "" {
			continue
		}
		var ids []uint
		if err := json.Unmarshal([]byte(g.VMIDs), &ids); err != nil {
			continue
		}
		members := make([]string, 0, len(ids))
		for _, id := range ids {
			if targetSet[id] {
				if hn, ok := nameByVM[id]; ok {
					members = append(members, hn)
				}
			}
		}
		if len(members) > 0 {
			out[g.Name] = members
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// uintSliceParam 取正整数切片参数（targets 等；容忍 json 反序列化出的 float64）。
func uintSliceParam(payload map[string]interface{}, key string) ([]uint, error) {
	raw, ok := payload[key].([]interface{})
	if !ok {
		return nil, fmt.Errorf("参数 %s 缺失或格式非法", key)
	}
	out := make([]uint, 0, len(raw))
	for _, item := range raw {
		switch v := item.(type) {
		case float64:
			if v <= 0 || v != float64(uint(v)) {
				return nil, fmt.Errorf("参数 %s 含非法值 %v", key, v)
			}
			out = append(out, uint(v))
		default:
			return nil, fmt.Errorf("参数 %s 含非法值 %v", key, item)
		}
	}
	return out, nil
}

// parseRecap 解析 PLAY RECAP 段（行形如 "host : ok=2 changed=1 unreachable=0 failed=0 ..."）。
func parseRecap(recapText string) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, line := range strings.Split(recapText, "\n") {
		idx := strings.Index(line, " : ")
		if idx <= 0 || !strings.Contains(line, "ok=") {
			continue
		}
		host := strings.TrimSpace(line[:idx])
		counts := map[string]int{}
		for _, kv := range strings.Fields(line[idx+3:]) {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				continue
			}
			n, err := strconv.Atoi(parts[1])
			if err == nil {
				counts[parts[0]] = n
			}
		}
		out[host] = counts
	}
	return out
}
