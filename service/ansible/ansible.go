// Package ansible：宿主机 ansible 引擎封装（P4 自动化运维 S1）。
// 平台不重新实现批量执行，只做三件事：探测 ansible/ansible-playbook、生成
// inventory、驱动子进程并回收输出。
//
// 目标白名单是结构性的：inventory 只由 tasks executor 从 vms 表登记的 VM 生成，
// 本包不接受任何调用方传入的裸 IP——想扫平台资产之外的地址，没有入口。
//
// 口令注入的取舍（与 AGENTS「明文口令不得落库」口径的关系）：ansible/sshpass 是
// 独立进程，无法共享平台内存；口令认证下唯一通道是把口令写进 inventory。处置：
// run 目录 0700、inventory 0600、执行完整个目录立即删除、审计不落口令——明文只在
// 磁盘上瞬时存在且随 run 消亡。S3 的 SSH key 免密方案落地后，新 VM 走 key 不再
// 经过这条路径（见 docs/plans/P4-自动化运维-Ansible.md）。
package ansible

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Engine 探测到的引擎：ansible 与 ansible-playbook 同目录（pip 布局）。
type Engine struct {
	Dir          string // 两个可执行文件所在目录
	PlaybookPath string
	AdhocPath    string
	Version      string // 如 "core 2.21.2"
}

var (
	detectMu sync.Mutex
	detected *Engine
)

// Detect 探测引擎（进程内缓存；找不到时每次重试——用户可能中途装好）。
// 探测顺序：PATH → ~/.local/bin（pip 用户级默认）→ /usr/local/bin → /usr/bin。
func Detect() (*Engine, error) {
	detectMu.Lock()
	defer detectMu.Unlock()
	if detected != nil {
		return detected, nil
	}
	var tried []string
	if p, err := exec.LookPath("ansible-playbook"); err == nil {
		if eng, perr := probe(filepath.Dir(p)); perr == nil {
			detected = eng
			return detected, nil
		} else {
			tried = append(tried, p+"("+perr.Error()+")")
		}
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{
		filepath.Join(home, ".local", "bin"),
		"/usr/local/bin",
		"/usr/bin",
	} {
		p := filepath.Join(dir, "ansible-playbook")
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			continue
		}
		eng, perr := probe(dir)
		if perr == nil {
			detected = eng
			return detected, nil
		}
		tried = append(tried, p+"("+perr.Error()+")")
	}
	if len(tried) > 0 {
		return nil, fmt.Errorf("探测到候选但不可执行: %s", strings.Join(tried, "; "))
	}
	return nil, errors.New("未找到 ansible-playbook（已探测 PATH、~/.local/bin、/usr/local/bin、/usr/bin）")
}

// probe 试跑 --version 确认可执行并取版本号（10s 兜底）。
func probe(dir string) (*Engine, error) {
	pb := filepath.Join(dir, "ansible-playbook")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, pb, "--version").Output()
	if err != nil {
		return nil, fmt.Errorf("--version 试跑失败: %w", err)
	}
	// 首行形如 "ansible-playbook [core 2.21.2]"，剥前缀取版本段
	ver := ""
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			ver = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "ansible-playbook"), "ansible"), " ")
			break
		}
	}
	return &Engine{Dir: dir, PlaybookPath: pb, AdhocPath: filepath.Join(dir, "ansible"), Version: ver}, nil
}

// HostEntry inventory 主机条目。Name 用 VM 名（平台约束 [a-zA-Z0-9_-]，可作 inventory
// 主机名）；同名校验由调用方去重（追加 -VMID 后缀）。
type HostEntry struct {
	Name string
	IP   string
	User string
	Port int
	Pass string
	// KeyFile 非 0 时走私钥免密通道（ansible_ssh_private_key_file），Pass 被忽略——
	// 平台公钥已注入的 VM（AnsibleReady）不再承载瞬时口令文件（P4-S4）
	KeyFile string
}

// BuildInventory 生成执行用 inventory（[all] 段 + 可选分组 children），返回文件路径。
func BuildInventory(dir string, hosts []HostEntry, groups map[string][]string) (string, error) {
	if len(hosts) == 0 {
		return "", errors.New("inventory 主机列表为空")
	}
	var b strings.Builder
	b.WriteString("[all]\n")
	for _, h := range hosts {
		if h.KeyFile != "" {
			fmt.Fprintf(&b, "%s ansible_host=%s ansible_port=%d ansible_user=%s ansible_ssh_private_key_file=%s\n",
				h.Name, h.IP, h.Port, h.User, h.KeyFile)
			continue
		}
		fmt.Fprintf(&b, "%s ansible_host=%s ansible_port=%d ansible_user=%s ansible_ssh_pass='%s'\n",
			h.Name, h.IP, h.Port, h.User, strings.ReplaceAll(h.Pass, "'", `'"'"'`))
	}
	// 成员白名单来自 [all] 主机：groups 只能引用已定义主机，防御性过滤
	names := map[string]bool{}
	for _, h := range hosts {
		names[h.Name] = true
	}
	for g, members := range groups {
		if g == "all" || len(members) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n[%s]\n", g)
		for _, m := range members {
			if names[m] {
				fmt.Fprintf(&b, "%s\n", m)
			}
		}
	}
	inv := filepath.Join(dir, "inventory")
	if err := os.WriteFile(inv, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("写 inventory 失败: %w", err)
	}
	return inv, nil
}

// RunOpts 执行参数。Playbook 与 Module 二选一：Playbook 非 0 跑 ansible-playbook，
// 否则按 adhoc 跑 ansible all -m Module [-a ModuleArgs]。
type RunOpts struct {
	Inventory  string
	Playbook   string
	Module     string
	ModuleArgs string
	// ExtraVars playbook 变量注入（S4 vars 表单化）：以 JSON 走 -e，优先级高于
	// playbook 内 vars 段（ansible 语义）
	ExtraVars map[string]string
	// Check 预演（--check）：只报会改什么，不真改（教学「先 check 再 apply」）
	Check bool
	// Diff 显示改动差异（--diff，仅 playbook；与 Check 搭配看「会改哪一行」）
	Diff bool
	// Tags / SkipTags 按标签只跑一段（-t / --skip-tags，仅 playbook）
	Tags     string
	SkipTags string
	// ConfigFile 指定 ansible.cfg（ANSIBLE_CONFIG 环境变量；空则用 ansible 默认发现）
	ConfigFile string
	Timeout    time.Duration
	OnLine     func(line string) // 逐行输出回调（executor 用于节流刷任务 Result）
}

// Run 驱动引擎执行，返回 RECAP 段原文（含逐主机 ok/changed/failed 计数）。
// 退出码语义：0=全部成功；2=部分主机失败/不可达（返回 error 且携带 RECAP）。
func (e *Engine) Run(ctx context.Context, opts RunOpts) (string, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	var cmd *exec.Cmd
	if opts.Playbook != "" {
		args := []string{"-i", opts.Inventory}
		if opts.Check {
			args = append(args, "--check")
		}
		if opts.Diff {
			args = append(args, "--diff")
		}
		if strings.TrimSpace(opts.Tags) != "" {
			args = append(args, "--tags", strings.TrimSpace(opts.Tags))
		}
		if strings.TrimSpace(opts.SkipTags) != "" {
			args = append(args, "--skip-tags", strings.TrimSpace(opts.SkipTags))
		}
		if len(opts.ExtraVars) > 0 {
			if raw, jerr := json.Marshal(opts.ExtraVars); jerr == nil {
				args = append(args, "-e", string(raw))
			}
		}
		args = append(args, opts.Playbook)
		cmd = exec.CommandContext(ctx, e.PlaybookPath, args...)
	} else {
		if opts.Module == "" {
			return "", errors.New("adhoc 模式需要指定模块")
		}
		args := []string{"all", "-m", opts.Module, "-i", opts.Inventory}
		if opts.ModuleArgs != "" {
			args = append(args, "-a", opts.ModuleArgs)
		}
		cmd = exec.CommandContext(ctx, e.AdhocPath, args...)
	}
	// HOST_KEY_CHECKING=False：受管 VM 的主机密钥 TOFU 已由 vmssh 层管理，ansible 侧
	// 对临时 inventory 关闭严格校验（VM 重建后指纹必变，逐台确认不现实）。
	// NOCOWS：宿主机装了 cowsay 时 ansible 会把 PLAY RECAP 画进牛对话框（`< PLAY RECAP >`），
	// 破坏输出与 RECAP 截取——关掉
	cmd.Env = append(os.Environ(),
		"ANSIBLE_HOST_KEY_CHECKING=False",
		"ANSIBLE_SSH_ARGS=-o UserKnownHostsFile=/dev/null -o ConnectTimeout=10",
		"ANSIBLE_FORCE_COLOR=0",
		"ANSIBLE_NOCOWS=1",
		"PYTHONUNBUFFERED=1",
	)
	// 平台生成的 run 级 ansible.cfg（forks/fact_caching/roles_path 等）
	if opts.ConfigFile != "" {
		cmd.Env = append(cmd.Env, "ANSIBLE_CONFIG="+opts.ConfigFile)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("创建输出管道失败: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("创建错误管道失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动 ansible 进程失败: %w", err)
	}

	// 双管道逐行合流到同一通道（WaitGroup 收口后统一 close，避免二次关闭）；
	// RECAP 段（PLAY RECAP 起）单独留存供结构化解析
	lines := make(chan string, 256)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scanInto(stdout, lines) }()
	go func() { defer wg.Done(); scanInto(stderr, lines) }()
	go func() { wg.Wait(); close(lines) }()

	var recap []string
	var tail []string // 输出尾部（错误时进错误链）
	inRecap := false
	collect := func(line string) {
		if opts.OnLine != nil {
			opts.OnLine(line)
		}
		if strings.Contains(line, "PLAY RECAP") { // 不用 HasPrefix：装饰性输出可能包住标记行
			inRecap = true
		}
		if inRecap {
			recap = append(recap, line)
		}
		tail = append(tail, line)
		if len(tail) > 200 {
			tail = tail[len(tail)-200:]
		}
	}
	for line := range lines {
		collect(line)
	}
	werr := cmd.Wait()
	recapText := strings.Join(recap, "\n")
	if werr == nil {
		return recapText, nil
	}
	var exitErr *exec.ExitError
	if errors.As(werr, &exitErr) && exitErr.ExitCode() == 2 {
		// 部分主机失败/不可达：RECAP 是关键诊断信息，直接进错误链
		return recapText, fmt.Errorf("部分主机执行失败:\n%s", recapText)
	}
	if ctx.Err() != nil {
		return recapText, fmt.Errorf("执行超时（%s）", opts.Timeout)
	}
	return recapText, fmt.Errorf("ansible 进程异常退出: %w\n%s", werr, strings.Join(tail[len(tail)-20:], "\n"))
}

// scanInto 把 reader 逐行扫进 channel（stdout/stderr 各一个；通道由 WaitGroup 收口方关闭）。
func scanInto(r io.Reader, lines chan<- string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines <- sc.Text()
	}
}

// SharedRolesDir 共享 role 库目录（data/ansible/roles）。装配层在启动时注入
// （与 cron.NotifyURL 同一手法：包级 var + main.go 赋值）。
//
// 两处都要用它，缺一处就会出现「保存时校验不过、执行时却能跑」的割裂：
//   - 执行：写进 run 级 ansible.cfg 的 roles_path
//   - 保存前 --syntax-check：经 ANSIBLE_ROLES_PATH 环境变量传给子进程
//
// 为空则不设置（退回 ansible 默认发现，此时共享 role 不可见）。
var SharedRolesDir string

// rolesEnv 共享 role 库对应的子进程环境（未配置时返回 nil，即不改动环境）。
func rolesEnv() []string {
	if SharedRolesDir == "" {
		return nil
	}
	return []string{"ANSIBLE_ROLES_PATH=" + SharedRolesDir}
}

// execCombined 跑命令并合并 stdout/stderr（SyntaxCheck 等短命令用）。
// 只用于 ansible-playbook：带上共享 role 库搜索路径，否则引用了共享 role 的 playbook
// 会在保存阶段被误判为「角色不存在」（实测：本地 roles/ 与 roles_path 都可见才判得对）。
func execCombined(ctx context.Context, name string, args ...string) (string, error) {
	return execCombinedEnv(ctx, rolesEnv(), name, args...)
}

// execCombinedEnv 同上，但由调用方指定追加的环境变量（共享 role 校验要指向自己的库目录）。
func execCombinedEnv(ctx context.Context, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
