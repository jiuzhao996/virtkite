// Package guestfs 封装 libguestfs 工具链（virt-sysprep / virt-customize / virt-sparsify），
// 供「模板制作 / 镜像清洗」在虚拟机**关机态**直接读写镜像文件：去个性化信息、注入基础优化、压缩。
//
// 权限现实：池卷属主是 libvirt-qemu，web 进程（jiuzhao）读不了也写不了，故一律经 sudo -n
// 调用——与 handler/vm_files_offline.go 的 guestmount 同一立场，宿主机需配置免密 sudo
// （本机 sudoers 为 NOPASSWD: ALL）。所有命令 exec.CommandContext 数组参数，禁止 shell 拼接。
package guestfs

import (
	"bufio"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// 工具二进制名（LookPath 用）。
const (
	binSysprep   = "virt-sysprep"
	binCustomize = "virt-customize"
	binSparsify  = "virt-sparsify"
)

// 默认超时：sysprep/customize 分钟级，sparsify 全盘拷贝更久。
const (
	detectProbeTimeout      = 20 * time.Second
	defaultSysprepTimeout   = 15 * time.Minute
	defaultCustomizeTimeout = 20 * time.Minute
	defaultSparsifyTimeout  = 40 * time.Minute
)

// Tool 探测到的 libguestfs 工具链。
type Tool struct {
	Sysprep   string // virt-sysprep 绝对路径
	Customize string // virt-customize 绝对路径
	Sparsify  string // virt-sparsify 绝对路径
	Version   string // virt-sysprep --version 首行
}

var (
	detectMu sync.Mutex
	detected *Tool
)

// Detect 探测工具链：LookPath 三个二进制 + **真跑一次 virt-sysprep --version 自检**。
// 只探二进制存在（如 handler/vm_files.go 的 guestmountAvailable）会在「二进制在、但
// libguestfs 起不来」（/boot/vmlinuz 不可读、appliance 构建失败等）时"看起来可用、一跑就崩"，
// 故这里必须试跑一次。进程内缓存；失败不缓存（用户可能中途装好）。
func Detect() (*Tool, error) {
	detectMu.Lock()
	defer detectMu.Unlock()
	if detected != nil {
		return detected, nil
	}
	sysprep, err := exec.LookPath(binSysprep)
	if err != nil {
		return nil, fmt.Errorf("未找到 %s（请安装 guestfs-tools / libguestfs-tools）: %w", binSysprep, err)
	}
	customize, err := exec.LookPath(binCustomize)
	if err != nil {
		return nil, fmt.Errorf("未找到 %s（请安装 guestfs-tools）: %w", binCustomize, err)
	}
	sparsify, err := exec.LookPath(binSparsify)
	if err != nil {
		return nil, fmt.Errorf("未找到 %s（请安装 guestfs-tools）: %w", binSparsify, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), detectProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, sysprep, "--version").Output()
	if err != nil {
		return nil, fmt.Errorf("%s --version 自检失败: %w", binSysprep, err)
	}
	ver := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	t := &Tool{Sysprep: sysprep, Customize: customize, Sparsify: sparsify, Version: ver}
	detected = t
	return t, nil
}

// Result 一次外部命令执行结果。
type Result struct {
	Output string   // 逐行合并后的完整输出
	Lines  []string // 逐行（stdout/stderr 合流）
}

// RunOpts 执行参数。
type RunOpts struct {
	Timeout time.Duration     // 0 = 用该命令的默认超时
	OnLine  func(line string) // 逐行回调（executor 用于节流上报进度）
}

// run 经 sudo -n 执行外部命令：双管道逐行收集（stdout/stderr 合流），支持 OnLine 回调与 ctx 取消。
func run(ctx context.Context, timeout time.Duration, bin string, args []string, opts RunOpts) (*Result, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sudo", append([]string{"-n", bin}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("创建输出管道失败: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("创建错误管道失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 %s 失败: %w", bin, err)
	}
	res := &Result{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	scan := func(rc io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			res.Lines = append(res.Lines, line)
			mu.Unlock()
			if opts.OnLine != nil {
				opts.OnLine(line)
			}
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	werr := cmd.Wait()
	wg.Wait()
	res.Output = strings.Join(res.Lines, "\n")
	if werr != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return res, fmt.Errorf("%s 执行超时: %w", bin, werr)
		}
		if ctx.Err() != nil {
			return res, fmt.Errorf("%s 已取消: %w", bin, ctx.Err())
		}
		if tail := tailLines(res.Lines, 5); tail != "" {
			return res, fmt.Errorf("%s 执行失败: %w\n%s", bin, werr, tail)
		}
		return res, fmt.Errorf("%s 执行失败: %w", bin, werr)
	}
	return res, nil
}

// tailLines 取末尾 n 行（失败时把关键报错带进错误链，全量输出可能很长）。
func tailLines(lines []string, n int) string {
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// buildSysprepArgs 组装 virt-sysprep 参数（纯函数，便于单测）。
// 默认跑全量清理操作；disableOps 非空时用 --disable 排除指定项（逗号分隔）。
func buildSysprepArgs(disk string, disableOps []string) []string {
	args := []string{"-a", disk}
	if len(disableOps) > 0 {
		args = append(args, "--disable", strings.Join(disableOps, ","))
	}
	return args
}

// Sysprep 清洗镜像：去 SSH 主机密钥 / bash 历史 / machine-id / udev 持久网卡等个性化信息，
// 让克隆出来的每台机器都是「干净」的。disk 必须是关机态镜像文件路径。**不可逆**。
func Sysprep(ctx context.Context, disk string, disableOps []string, opts RunOpts) (*Result, error) {
	t, err := Detect()
	if err != nil {
		return nil, err
	}
	return run(ctx, defaultSysprepTimeout, t.Sysprep, buildSysprepArgs(disk, disableOps), opts)
}

// buildCustomizeArgs 组装 virt-customize 参数（纯函数）。
// sshPubKeyPath 非空时用 --ssh-inject 把平台公钥烘进 guest 的 authorized_keys——
// 让模板克隆出的机器开机即被平台免密接管（不依赖 cloud-init 是否生效）。
func buildCustomizeArgs(disk, scriptPath, sshPubKeyPath string) []string {
	args := []string{"-a", disk, "--run", scriptPath}
	if sshPubKeyPath != "" {
		args = append(args, "--ssh-inject", "root:file:"+sshPubKeyPath)
	}
	return args
}

// Customize 对镜像注入基础优化（--run 让脚本在 guest 内执行）并（可选）注入平台 SSH 公钥。
func Customize(ctx context.Context, disk, scriptPath, sshPubKeyPath string, opts RunOpts) (*Result, error) {
	t, err := Detect()
	if err != nil {
		return nil, err
	}
	return run(ctx, defaultCustomizeTimeout, t.Customize, buildCustomizeArgs(disk, scriptPath, sshPubKeyPath), opts)
}

// buildSparsifyArgs 组装 virt-sparsify 参数（纯函数）。
func buildSparsifyArgs(disk string) []string {
	return []string{"--in-place", disk}
}

// Sparsify 原地压缩镜像（回收未使用块）。慢（全盘拷贝），超时较长。
func Sparsify(ctx context.Context, disk string, opts RunOpts) (*Result, error) {
	t, err := Detect()
	if err != nil {
		return nil, err
	}
	return run(ctx, defaultSparsifyTimeout, t.Sparsify, buildSparsifyArgs(disk), opts)
}

//go:embed optimize/common.sh
var optCommon string

//go:embed optimize/rhel.sh
var optRHEL string

//go:embed optimize/debian.sh
var optDebian string

// OptimizeScript 组装基础优化脚本：公共段 + 按发行版族分支（RHEL 系 / Debian 系，在 guest 内
// 用包管理器自动识别）+ 可选自定义段。
// custom 非空时追加执行——用户自带 base_config.sh 可直接塞进来做镜像专属优化。
func OptimizeScript(custom string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# 由 vmops 模板固化生成（公共段 + 发行版族 + 自定义段）\nset +e\n")
	b.WriteString("log() { echo \"[vmops-optimize] $*\"; }\n\n")
	b.WriteString(optCommon)
	b.WriteString("\n# ── 发行版族专属（按包管理器识别）──\n")
	b.WriteString("if command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then\n")
	b.WriteString(optRHEL)
	b.WriteString("\nelif command -v apt-get >/dev/null 2>&1; then\n")
	b.WriteString(optDebian)
	b.WriteString("\nelse\n  log \"未识别包管理器，跳过发行版专属优化\"\nfi\n")
	if strings.TrimSpace(custom) != "" {
		b.WriteString("\n# ── 自定义优化段（用户提供）──\n")
		b.WriteString(custom)
		if !strings.HasSuffix(custom, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString("\nlog \"基础优化完成（best-effort）\"\nexit 0\n")
	return b.String()
}
