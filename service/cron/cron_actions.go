// 本文件负责计划任务各「动作」的具体执行实现（快照 / 数据库备份 / ansible playbook /
// 容器健康巡检 / 镜像版本巡检 / 设计器漂移巡检）及其私有助手、动作通知，以及按动作类型
// 分发的入口 runAction。调度与落库（什么时候跑、跑完怎么记）在 cron.go。
package cron

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/imgver"
	"github.com/jiuzhao/vmops/service/notify"
	"github.com/jiuzhao/vmops/service/virt"
)

// 动作类型白名单（model.ScheduledTask.Action 的合法取值）。
const (
	ActionVMSnapshot           = "vm_snapshot"           // 定时对指定虚拟机打快照
	ActionDBBackup             = "db_backup"             // 定时备份数据库（mysqldump）
	ActionAnsiblePlaybook      = "ansible_playbook"      // 定时执行 playbook（P4-S3：调度×引擎缝合）
	ActionContainerHealthcheck = "container_healthcheck" // 容器健康巡检：unhealthy 自动重启（R8）
	ActionImageVersionCheck    = "image_version_check"   // 镜像版本巡检：outdated 时通知（R9）
	// ActionDesignerDrift 设计器漂移巡检（DE2）：快照 vs 现实，差异进 cron_runs + 通知
	ActionDesignerDrift = "designer_drift_check"
)

// AnsiblePlaybookMaxWait 定时 playbook 执行的兜底等待：转任务异步跑，这里轮询终态。
const AnsiblePlaybookMaxWait = 25 * time.Minute

// AnsiblePlaybookIDRe playbook 文件 id 白名单（handler 校验与包内执行共用同一规约）。
var AnsiblePlaybookIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

const (
	// defaultBackupDir 数据库备份默认输出目录（Scheduler.BackupDir 为空时使用）。
	defaultBackupDir = "/home/jiuzhao/vmops/data/backup"
	// dbContainerName 备份目标容器：mysqldump 在 MySQL 容器内执行（宿主机不装 mysql 客户端）。
	dbContainerName = "vmops-mysql"
	// defaultDBUser / defaultDBName：配置缺失或 config 未初始化（如单测）时的兜底，与 config 默认值一致。
	defaultDBUser = "vmops"
	defaultDBName = "vmops"
	// mysqlPwdEnv 口令传给 mysqldump 的环境变量名：mysqldump 原生识别，避免口令出现在命令行
	// 参数里（容器内 ps / docker inspect 都能看到 -p<明文>）。
	mysqlPwdEnv = "MYSQL_PWD"
	// dbBackupTimeout mysqldump 最长执行时间，防容器假死把调度协程吊死。
	dbBackupTimeout = 10 * time.Minute
	// snapshotPrefix 定时快照的命名前缀；保留份数清理只针对该前缀，手工快照不受影响。
	snapshotPrefix = "cron-"
)

// runAction 按动作类型分发执行，返回摘要与错误。
func (s *Scheduler) runAction(st model.ScheduledTask) (string, error) {
	switch st.Action {
	case ActionVMSnapshot:
		return s.runVMSnapshot(st)
	case ActionDBBackup:
		return s.runDBBackup(st)
	case ActionAnsiblePlaybook:
		return s.runAnsiblePlaybook(st)
	case ActionContainerHealthcheck:
		return s.runContainerHealthcheck(st)
	case ActionImageVersionCheck:
		return s.runImageVersionCheck(st)
	case ActionDesignerDrift:
		return s.runDesignerDrift(st)
	default:
		return "", fmt.Errorf("未知动作类型 %q", st.Action)
	}
}

// runContainerHealthcheck 容器健康巡检：params JSON 可选 {"notify":true}。
// 流程：docker ps 取运行中容器 → 单次 docker inspect 批量取 State.Health →
// 门限 unhealthy 且 FailingStreak>=3 → docker restart → 通知 + 审计 → 摘要。
// 选 cron action 而非后台常驻 loop：调度/历史/手动触发/启停/失败通知五件套白拿，
// 「看得见的机制」优于「看不见的魔法」；健康检查秒级完成，不转任务管线（避免每分钟
// 一条任务把 TaskList 刷成噪音——这与 ansible_playbook 转任务的取舍有意不同）。
func (s *Scheduler) runContainerHealthcheck(st model.ScheduledTask) (string, error) {
	notifyOn := true
	if strings.TrimSpace(st.Params) != "" {
		var params struct {
			Notify *bool `json:"notify"`
		}
		if err := json.Unmarshal([]byte(st.Params), &params); err == nil && params.Notify != nil {
			notifyOn = *params.Notify
		}
	}

	running, err := s.listRunningContainers()
	if err != nil {
		return "", fmt.Errorf("列举容器失败: %w", err)
	}
	if len(running) == 0 {
		return "无运行中容器，无需巡检", nil
	}
	healths, err := s.inspectContainerHealth(running)
	if err != nil {
		return "", fmt.Errorf("查询容器健康状态失败: %w", err)
	}

	var restarted []string
	for _, h := range healths {
		// FailingStreak>=3 门闩：docker 侧已连续失败 3 次才动手，防抖防重启风暴
		if h.Status != "unhealthy" || h.FailingStreak < 3 {
			continue
		}
		if err := s.restartContainer(h.ID); err != nil {
			log.Printf("[cron] 健康自愈重启失败 container=%s(%s): %v", h.Name, h.ID, err)
			continue
		}
		restarted = append(restarted, h.Name)
		log.Printf("[cron] 容器健康自愈：已重启 %s(%s) 连续失败 %d 次", h.Name, h.ID, h.FailingStreak)
		s.auditHealthRestart(h, st)
	}

	summary := fmt.Sprintf("巡检 %d 个运行中容器，unhealthy %d 个，已重启 %d 个",
		len(running), countUnhealthy(healths), len(restarted))
	if len(restarted) > 0 {
		summary += "：" + strings.Join(restarted, "、")
		if notifyOn {
			notifyHealth(st.Name, summary)
		}
	}
	return summary, nil
}

// containerHealth 单容器健康状态（docker inspect --format '{{json .State.Health}}'）。
type containerHealth struct {
	ID            string
	Name          string
	Status        string // healthy / unhealthy / starting / none
	FailingStreak int
}

func countUnhealthy(hs []containerHealth) int {
	n := 0
	for _, h := range hs {
		if h.Status == "unhealthy" {
			n++
		}
	}
	return n
}

// listRunningContainers 返回运行中容器的 id/name（docker ps --format json）。
func (s *Scheduler) listRunningContainers() ([]containerBasic, error) {
	out, err := exec.Command("docker", "ps", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	var list []containerBasic
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c struct {
			ID    string `json:"ID"`
			Names string `json:"Names"`
		}
		if json.Unmarshal([]byte(line), &c) != nil {
			continue
		}
		list = append(list, containerBasic{ID: c.ID, Name: strings.TrimPrefix(c.Names, "/")})
	}
	return list, nil
}

// containerBasic 巡检所需的容器最小信息。
type containerBasic struct {
	ID   string
	Name string
}

// inspectContainerHealth 单次进程批量取健康状态（docker inspect --format '{{json .State.Health}}' id1 id2…）。
// 无健康检查的容器（State.Health 为 null）返回 Status=none，不参与重启判定。
func (s *Scheduler) inspectContainerHealth(containers []containerBasic) ([]containerHealth, error) {
	args := []string{"inspect", "--format", "{{json .State.Health}}"}
	nameByID := map[string]string{}
	for _, c := range containers {
		args = append(args, c.ID)
		nameByID[c.ID[:12]] = c.Name
	}
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil, err
	}
	var result []containerHealth
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "null" {
			continue
		}
		var h struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
		}
		if json.Unmarshal([]byte(line), &h) != nil {
			continue
		}
		result = append(result, containerHealth{Status: h.Status, FailingStreak: h.FailingStreak})
	}
	// docker inspect 多目标输出顺序与入参一致：按序回填 id/name
	for i := range result {
		if i < len(containers) {
			result[i].ID = containers[i].ID
			result[i].Name = containers[i].Name
		}
	}
	return result, nil
}

// restartContainer 重启容器（等价 docker restart）。
func (s *Scheduler) restartContainer(id string) error {
	return exec.Command("docker", "restart", id).Run()
}

// auditHealthRestart 容器自愈动作落审计（照 jumpd 手动落行先例：后台动作不经 HTTP 中间件）。
func (s *Scheduler) auditHealthRestart(h containerHealth, st model.ScheduledTask) {
	audit := model.AuditLog{
		Username:   "system",
		Action:     "container_healthcheck",
		ObjectType: "docker",
		Detail:     fmt.Sprintf("健康自愈：重启容器 %s（连续失败 %d 次，计划任务「%s」）", h.Name, h.FailingStreak, st.Name),
		Status:     "ok",
		CreatedAt:  time.Now(),
	}
	if err := s.DB.Create(&audit).Error; err != nil {
		log.Printf("[cron] 健康自愈审计落行失败 container=%s: %v", h.Name, err)
	}
}

// notifyHealth 健康自愈通知（经 NotifyURL 推送；为空则不推）。
func notifyHealth(taskName, summary string) {
	url := NotifyURL
	if url == "" {
		return
	}
	text := fmt.Sprintf("[鸢航VirtKite] 容器健康自愈: %s %s", taskName, summary)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cron] 健康自愈通知协程 panic: %v\n%s", r, debug.Stack())
			}
		}()
		if err := notify.SendText(url, text); err != nil {
			log.Printf("[cron] 健康自愈通知推送失败: %v", err)
		}
	}()
}

// runImageVersionCheck 镜像版本巡检（R9）：复用 imgver 的 digest 对比，
// outdated>0 时经 NotifyURL 推送镜像名列表（截前 10）。检查本身只读，失败即报错。
func (s *Scheduler) runImageVersionCheck(st model.ScheduledTask) (string, error) {
	d := dockerx.New()
	runner := &imgver.Runner{
		ListImages: func() ([][2]string, error) {
			imgs, err := d.Images()
			if err != nil {
				return nil, err
			}
			out := make([][2]string, 0, len(imgs))
			for _, im := range imgs {
				out = append(out, [2]string{im.Repository, im.Tag})
			}
			return out, nil
		},
		LocalDigest: func(image string) string {
			info, err := d.Inspect(image)
			if err != nil {
				return ""
			}
			if arr, ok := info["RepoDigests"].([]interface{}); ok {
				for _, dg := range arr {
					if sv, ok := dg.(string); ok && strings.Contains(sv, "@sha256:") {
						return sv[strings.Index(sv, "@sha256:")+len("@sha256:"):]
					}
				}
			}
			return ""
		},
	}
	items, err := runner.CheckAll()
	if err != nil {
		return "", fmt.Errorf("镜像版本检测失败: %w", err)
	}
	outdated := imgver.Outdated(items)
	if len(outdated) == 0 {
		return fmt.Sprintf("巡检 %d 个镜像，全部为最新", len(items)), nil
	}
	shown := outdated
	extra := ""
	if len(outdated) > 10 {
		shown = outdated[:10]
		extra = fmt.Sprintf(" 等 %d 个", len(outdated))
	}
	summary := fmt.Sprintf("巡检 %d 个镜像，%d 个有更新：%s%s", len(items), len(outdated), strings.Join(shown, "、"), extra)
	notifyVersion(summary)
	return summary, nil
}

// runDesignerDrift 设计器漂移巡检（DE2）：调注入的对比回调，有漂移则通知并进摘要。
// 回调未注入（非 routes 装配路径，如单测）时如实报错，不静默成功。
func (s *Scheduler) runDesignerDrift(st model.ScheduledTask) (string, error) {
	if s.DriftCheck == nil {
		return "", fmt.Errorf("漂移巡检未接入（DriftCheck 回调未注入）")
	}
	checked, drifted, summary := s.DriftCheck()
	if drifted > 0 {
		notifyDrift(summary)
	}
	return fmt.Sprintf("已检查 %d 个计划，漂移 %d 个\n%s", checked, drifted, summary), nil
}

// notifyDrift 漂移告警推送（经 NotifyURL；为空则不推）。
func notifyDrift(summary string) {
	url := NotifyURL
	if url == "" {
		return
	}
	text := "[鸢航VirtKite] 架构漂移告警\n" + truncateRunes(summary, notifyErrLimit*4)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cron] 漂移通知协程 panic: %v\n%s", r, debug.Stack())
			}
		}()
		if err := notify.SendText(url, text); err != nil {
			log.Printf("[cron] 漂移通知推送失败: %v", err)
		}
	}()
}

// notifyVersion 镜像更新通知（经 NotifyURL 推送；为空则不推）。
func notifyVersion(summary string) {
	url := NotifyURL
	if url == "" {
		return
	}
	text := "[鸢航VirtKite] 镜像版本巡检: " + summary
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cron] 镜像版本通知协程 panic: %v\n%s", r, debug.Stack())
			}
		}()
		if err := notify.SendText(url, text); err != nil {
			log.Printf("[cron] 镜像版本通知推送失败: %v", err)
		}
	}()
}

// runVMSnapshot 执行定时快照：params JSON 形如 {"vm_id":14}，快照名 cron-<时间戳>。
func (s *Scheduler) runVMSnapshot(st model.ScheduledTask) (string, error) {
	var params struct {
		VMID uint `json:"vm_id"`
	}
	if strings.TrimSpace(st.Params) == "" {
		return "", fmt.Errorf("参数为空，vm_snapshot 需要 {\"vm_id\":N}")
	}
	if err := json.Unmarshal([]byte(st.Params), &params); err != nil {
		return "", fmt.Errorf("解析参数失败: %w", err)
	}
	if params.VMID == 0 {
		return "", fmt.Errorf("参数缺少有效的 vm_id")
	}
	var vm model.VM
	if err := s.DB.First(&vm, params.VMID).Error; err != nil {
		return "", fmt.Errorf("虚拟机 %d 不存在: %w", params.VMID, err)
	}
	snapName := snapshotPrefix + time.Now().Format("20060102-150405")
	if err := s.Virt.CreateSnapshot(vm.Name, snapName, "计划任务自动快照"); err != nil {
		return "", fmt.Errorf("创建快照失败: %w", err)
	}
	log.Printf("[cron] 快照创建成功 vm=%s(%d) snapshot=%s", vm.Name, vm.ID, snapName)

	summary := fmt.Sprintf("快照 %s 创建成功", snapName)
	// 保留份数：超出部分的旧 cron- 快照按创建时间从老到新删除（best-effort，不阻断成功语义）
	if removed := s.pruneVMSnapshots(vm.Name, keepOrDefault(st.Keep)); removed > 0 {
		summary += fmt.Sprintf("，已清理过期快照 %d 份", removed)
	}
	return summary, nil
}

// pruneVMSnapshots 只保留最近 keep 份 cron- 前缀快照，更老的按创建时间从新到旧排序后
// 删除（即最老的先删）。只动 snapshotPrefix 前缀，手工创建的快照不受影响。
// 清理属 best-effort：列举失败或单个删除失败只记日志继续，不清理干净不影响本次
// 快照已成功的语义。返回实际删除份数（供摘要文案）。
func (s *Scheduler) pruneVMSnapshots(vmName string, keep int) int {
	snaps, err := s.Virt.ListSnapshots(vmName)
	if err != nil {
		log.Printf("[cron] 列举快照失败 vm=%s: %v", vmName, err)
		return 0
	}
	cronSnaps := make([]virt.SnapshotInfo, 0, len(snaps))
	for _, sn := range snaps {
		if strings.HasPrefix(sn.Name, snapshotPrefix) {
			cronSnaps = append(cronSnaps, sn)
		}
	}
	if len(cronSnaps) <= keep {
		return 0
	}
	// 创建时间倒序（新的在前）；同秒冲突按名字字典序兜底，保证删除顺序确定
	sort.Slice(cronSnaps, func(i, j int) bool {
		if cronSnaps[i].CreationTime != cronSnaps[j].CreationTime {
			return cronSnaps[i].CreationTime > cronSnaps[j].CreationTime
		}
		return cronSnaps[i].Name > cronSnaps[j].Name
	})
	removed := 0
	for _, sn := range cronSnaps[keep:] {
		if err := s.Virt.DeleteSnapshot(vmName, sn.Name); err != nil {
			log.Printf("[cron] 清理过期快照失败 vm=%s snapshot=%s: %v", vmName, sn.Name, err)
			continue
		}
		log.Printf("[cron] 已清理过期快照 vm=%s snapshot=%s", vmName, sn.Name)
		removed++
	}
	return removed
}

// runAnsiblePlaybook 定时执行 playbook（P4-S3）：params 形如
// {"playbook":"sysctl-tuning","targets":[89,90]} 或 {"playbook":"...","group_id":3}
// （group_id 走主机组，成员动态生效——改组即生效，不再存字面 targets）。
// 转 ansible_run 异步任务执行（凭据/inventory/输出全在任务管线），提交后轮询终态并汇总 RECAP 进执行历史。
// 调度与引擎各司其职：cron 回答「什么时候跑」，ansible_run 回答「怎么跑」。
func (s *Scheduler) runAnsiblePlaybook(st model.ScheduledTask) (string, error) {
	if s.TaskMgr == nil {
		return "", fmt.Errorf("任务系统未接入（TaskMgr 为空）")
	}
	var params struct {
		Playbook string `json:"playbook"`
		Targets  []uint `json:"targets"`
		GroupID  uint   `json:"group_id"`
	}
	if err := json.Unmarshal([]byte(st.Params), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if params.Playbook == "" || (len(params.Targets) == 0 && params.GroupID == 0) {
		return "", fmt.Errorf("参数不完整（需要 playbook 与 targets 或 group_id）")
	}
	payload := map[string]interface{}{"playbook": params.Playbook}
	if len(params.Targets) > 0 {
		payload["targets"] = params.Targets
	}
	if params.GroupID > 0 {
		payload["group_id"] = params.GroupID
	}

	username := "cron"
	task, err := s.TaskMgr.Submit("ansible_run",
		fmt.Sprintf("计划任务 %s（playbook %s）", st.Name, params.Playbook),
		payload, nil, username, "", nil)
	if err != nil {
		return "", fmt.Errorf("提交 ansible_run 任务失败: %w", err)
	}

	// 轮询终态（execute 持有 execMu，此处分钟级等待会让并发的其他计划任务排队——
	// 当前规模单机 few jobs，可接受；作业多了再改非阻塞）
	deadline := time.Now().Add(AnsiblePlaybookMaxWait)
	for {
		time.Sleep(5 * time.Second)
		var t model.Task
		if qerr := s.DB.First(&t, task.ID).Error; qerr != nil {
			return "", fmt.Errorf("读取任务状态失败: %w", qerr)
		}
		switch t.Status {
		case model.TaskStatusSuccess:
			return s.summarizeAnsibleRun(params.Playbook, t.Result)
		case model.TaskStatusFailed:
			msg := t.Error
			if msg == "" {
				msg = "任务失败（无错误详情）"
			}
			return "", fmt.Errorf("playbook %s 执行失败: %s", params.Playbook, msg)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("等待任务 %d 超时（%s）", task.ID, AnsiblePlaybookMaxWait)
		}
	}
}

// summarizeAnsibleRun 从任务 Result JSON 提取 RECAP 计数做执行历史摘要。
func (s *Scheduler) summarizeAnsibleRun(playbook, result string) (string, error) {
	var res struct {
		Recap map[string]map[string]int `json:"recap"`
	}
	failedTotal := 0
	hostLines := make([]string, 0, len(res.Recap))
	if result != "" {
		if err := json.Unmarshal([]byte(result), &res); err == nil {
			hosts := make([]string, 0, len(res.Recap))
			for h := range res.Recap {
				hosts = append(hosts, h)
			}
			sort.Strings(hosts)
			for _, h := range hosts {
				c := res.Recap[h]
				hostLines = append(hostLines, fmt.Sprintf("%s ok=%d changed=%d failed=%d unreachable=%d",
					h, c["ok"], c["changed"], c["failed"], c["unreachable"]))
				failedTotal += c["failed"] + c["unreachable"]
			}
		}
	}
	summary := fmt.Sprintf("playbook %s 执行完成（task 已入库）", playbook)
	if len(hostLines) > 0 {
		summary += ": " + strings.Join(hostLines, "; ")
	}
	if failedTotal > 0 {
		return summary, fmt.Errorf("playbook %s 有 %d 台主机失败", playbook, failedTotal)
	}
	return summary, nil
}

// runDBBackup 执行数据库备份：docker exec 进 MySQL 容器跑 mysqldump，
// 输出落 BackupDir/vmops-YYYYMMDD-HHMM.sql，并只保留最近 keep（st.Keep，<=0 视为 7）份。
// 返回成果摘要供执行历史 output 使用。
func (s *Scheduler) runDBBackup(st model.ScheduledTask) (string, error) {
	dir := s.BackupDir
	if dir == "" {
		dir = defaultBackupDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	outPath := filepath.Join(dir, "vmops-"+time.Now().Format("20060102-1504")+".sql")
	f, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("创建备份文件失败: %w", err)
	}
	defer f.Close()

	// 备份用的库账号与口令一律取自全局配置（DB_USER / DB_NAME / DB_PASSWORD），不再写死：
	// 硬编码口令会与配置形成隐式耦合——运维按配置改了库口令后，定时任务静默失败（错误只进
	// 任务日志），真要恢复时才发现没有一份可用备份。
	user, dbName, password := dbBackupCreds()

	ctx, cancel := context.WithTimeout(context.Background(), dbBackupTimeout)
	defer cancel()
	// 参数必须走数组形式（不经过 shell 拼接）；等价命令：
	//   docker exec -e MYSQL_PWD=*** vmops-mysql mysqldump -uvmops vmops
	args := []string{"exec"}
	if password != "" {
		args = append(args, "-e", mysqlPwdEnv+"="+password)
	} else {
		// 口令为空时不能拼出裸 "-p"：mysqldump 会把它当成「等待交互输入口令」而挂到超时，
		// 表现为备份任务假死且无输出，比直接报连接失败更难排查。
		log.Printf("[cron] 警告：DB_PASSWORD 为空，mysqldump 将以无口令方式连接；若库要求口令请配置 DB_PASSWORD")
	}
	args = append(args, dbContainerName, "mysqldump", "-u"+user, dbName)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = f
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		// errBuf 可能回显命令片段，故只截取有限长度；口令经 env 传递不会出现在命令行里
		return "", fmt.Errorf("mysqldump 执行失败: %w（stderr: %s）", err, truncateRunes(strings.TrimSpace(errBuf.String()), 500))
	}
	log.Printf("[cron] 数据库备份完成 file=%s", outPath)

	keep := keepOrDefault(st.Keep)
	s.pruneBackups(dir, keep)
	return fmt.Sprintf("备份完成 %s（已保留最近 %d 份）", outPath, keep), nil
}

// dbBackupCreds 取备份用的数据库账号、库名与口令（供 runDBBackup 拼 mysqldump）。
// config 未初始化（单测或独立调用）时回退到保守默认值，避免 nil 解引用 panic。
func dbBackupCreds() (user, dbName, password string) {
	user, dbName = defaultDBUser, defaultDBName
	if config.GlobalConfig == nil {
		return user, dbName, ""
	}
	if config.GlobalConfig.DBUser != "" {
		user = config.GlobalConfig.DBUser
	}
	if config.GlobalConfig.DBName != "" {
		dbName = config.GlobalConfig.DBName
	}
	return user, dbName, config.GlobalConfig.DBPassword
}

// pruneBackups 只保留最近 keep 份备份（文件名内嵌时间戳，字典序即时间序）。
// 清理属 best-effort，失败只记日志——删不掉旧文件不影响本次备份的有效性。
func (s *Scheduler) pruneBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("[cron] 读取备份目录失败 dir=%s: %v", dir, err)
		return
	}
	backups := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, "vmops-") && strings.HasSuffix(name, ".sql") {
			backups = append(backups, name)
		}
	}
	if len(backups) <= keep {
		return
	}
	sort.Strings(backups) // vmops-YYYYMMDD-HHMM.sql 字典序 == 时间序
	for _, name := range backups[:len(backups)-keep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			log.Printf("[cron] 清理过期备份失败 file=%s: %v", name, err)
			continue
		}
		log.Printf("[cron] 已清理过期备份 file=%s", name)
	}
}

// keepOrDefault 归一化保留份数：未配置（<=0，含 gorm default 未生效的历史行零值）回退 DefaultKeep。
func keepOrDefault(keep int) int {
	if keep <= 0 {
		return DefaultKeep
	}
	return keep
}
