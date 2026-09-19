// Package vmssh 提供面向「虚拟机文件管理」的 SSH 远程执行封装。
//
// 与 handler/terminal.go 的交互式终端桥不同，本包面向一次性命令执行（列目录/读文件/
// 写文件/删除/建目录）：每次调用新建连接、执行完即关（短连接），无需维护会话状态。
// 复用 golang.org/x/crypto/ssh（与 terminal.go 同一依赖，不引入新库）。
//
// 安全约定：
//   - HostKeyCallback 走 TOFU 主机密钥校验（hostkey.go，等价 openssh 首连记录
//     known_hosts、后续比对指纹）：目标是平台自建、IP 由 DHCP 动态分配的短生命周期
//     虚拟机，重建即换主机密钥，人工核对 known_hosts 不可操作，故采用自动 TOFU；
//     目标范围另由调用方（handler 层 validateSSHTarget 白名单）收敛到本机私有网段。
//   - VM 内命令的路径参数必须经 ShellQuote 包裹后再拼接，禁止任何用户输入直接拼进命令串。
package vmssh

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// DefaultTimeout 单条命令的默认执行超时（含拨号与命令执行全程）。
const DefaultTimeout = 15 * time.Second

// Options SSH 连接参数（口令仅内存传递，不落盘、不进日志，与 Web 终端一致）。
type Options struct {
	Host     string // 目标 IP
	Port     int    // SSH 端口，<=0 时取默认 22
	User     string
	Password string
}

// runResult 单次命令执行的完整结果（缓冲区归 goroutine 私有，经通道移交，
// 超时路径下主流程不再触碰缓冲区，避免与仍在收尾的 goroutine 产生数据竞争）。
type runResult struct {
	stdout string
	stderr string
	err    error
}

// Run 在目标主机上执行 cmd，返回 stdout 与 stderr（两者分开接收，互不混入）。
// timeout <=0 时取 DefaultTimeout。
func Run(opts Options, cmd string, timeout time.Duration) (stdout string, stderr string, err error) {
	return RunWithStdin(opts, cmd, nil, timeout)
}

// RunWithStdin 在 Run 的基础上向命令的 stdin 喂入 stdin 内容（nil 表示不写 stdin），
// 供「cat > 路径」式的文件写入使用。命令非零退出返回 *ssh.ExitError，stderr 带远程报错原文。
func RunWithStdin(opts Options, cmd string, stdin io.Reader, timeout time.Duration) (stdout string, stderr string, err error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if opts.Port <= 0 {
		opts.Port = 22
	}

	config := &ssh.ClientConfig{
		User: opts.User,
		Auth: []ssh.AuthMethod{ssh.Password(opts.Password)},
		// 主机密钥 TOFU 校验（与 handler/terminal.go 同一回调工厂），论证见包注释（安全约定）；
		// 中间人风险由指纹比对 + 调用方的 validateSSHTarget 私有网段白名单双重收敛。
		HostKeyCallback: TOFUHostKeyCallback(),
		Timeout:         8 * time.Second, // TCP 拨号超时（ssh.Dial 内部用它做 net.DialTimeout）
	}

	client, err := ssh.Dial("tcp", net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)), config)
	if err != nil {
		// 完整错误（可能含内网拓扑 / banner）交由调用方记服务端日志，链路必须保留
		return "", "", fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("SSH 会话创建失败: %w", err)
	}
	defer session.Close()

	if stdin != nil {
		w, err := session.StdinPipe()
		if err != nil {
			return "", "", fmt.Errorf("获取 stdin 管道失败: %w", err)
		}
		go func() {
			// 自起 goroutine 必须自带 recover（gin Recovery 只覆盖 HTTP 请求链）
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("[vmssh] stdin 写入 panic host=%s:%d panic=%v\n%s", opts.Host, opts.Port, rec, debug.Stack())
				}
			}()
			defer w.Close() // 写完关管道，远端 cat 才能收到 EOF 正常退出
			_, _ = io.Copy(w, stdin)
		}()
	}

	var outBuf, errBuf bytes.Buffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	// x/crypto/ssh 的 Session.Run 不支持 context，超时用「goroutine 执行 + select 等待」实现；
	// 超时后关闭底层连接令 session.Run 立即失败返回，执行 goroutine 随之退出，不会泄漏。
	done := make(chan runResult, 1) // 带缓冲：超时路径返回后 goroutine 仍可投递结果并退出
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				done <- runResult{err: fmt.Errorf("SSH 执行异常: %v", rec)}
			}
		}()
		err := session.Run(cmd)
		done <- runResult{stdout: outBuf.String(), stderr: errBuf.String(), err: err}
	}()

	select {
	case r := <-done:
		return r.stdout, r.stderr, r.err
	case <-time.After(timeout):
		client.Close()
		return "", "", fmt.Errorf("SSH 命令执行超时（超过 %s）", timeout)
	}
}

// ShellQuote 对字符串做 POSIX 单引号包裹，用于把路径等不可信输入安全拼进远程命令。
// 单引号内除「'」本身外一切字符（含 $ ` \ 空格 换行）均失去特殊含义；内部单引号按
// POSIX 惯例转为 '\”（闭合、转义引号、重开），等价 shell 引用规则。
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
