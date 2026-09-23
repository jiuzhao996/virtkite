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
	"errors"
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
//
// 字段一律不可导出，是刻意的：外部无法用字面量自行拼装，唯一出口是 NewOptions，
// 而 NewOptions 强制过 ValidateTarget 白名单。
//
// 背景（胰腺癌级教训）：SSH 拨号参数完全来自浏览器请求体，校验原本分散在各个
// handler 调用点「记得就调一次」。新增应用安装模块时漏了一行，平台直接变成免费
// 跳板机（内网横扫/爆破/投递脚本，源 IP 全算在服务器上），而代码读起来完全正常、
// 测试也全绿。把校验下沉进构造函数后，「绕过白名单外连任意地址」从「靠人记得」
// 变成了「编译期做不到」。
type Options struct {
	host      string
	port      int
	user      string
	password  string
	validated bool // 是否经 NewOptions 校验；零值为 false，拨号入口一律拒绝
}

// NewOptions 构造连接参数并强制做拨号目标白名单校验（ValidateTarget）。
// recordedIP 为平台登记的该虚拟机 IP（vm.IP），空值时退回「仅私有网段」口径。
// port <= 0 时取默认 22。
func NewOptions(recordedIP, host string, port int, user, password string) (Options, error) {
	if port <= 0 {
		port = 22
	}
	// 先补默认端口再校验：ValidateTarget 要求端口落在 1-65535
	if err := ValidateTarget(recordedIP, host, port); err != nil {
		return Options{}, err
	}
	return Options{host: host, port: port, user: user, password: password, validated: true}, nil
}

// Host / Port / User 只读访问器（口令不外露：只在本包内用于组装 ClientConfig）。
func (o Options) Host() string { return o.host }
func (o Options) Port() int    { return o.port }
func (o Options) User() string { return o.user }

// Addr 返回 host:port 形式的拨号地址（IPv6 自动加方括号）。
func (o Options) Addr() string { return net.JoinHostPort(o.host, strconv.Itoa(o.port)) }

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

// clientConfig 组装 SSH 客户端配置（主机密钥 TOFU 校验，论证见包注释安全约定）。
func clientConfig(opts Options) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User: opts.user,
		Auth: []ssh.AuthMethod{ssh.Password(opts.password)},
		// 主机密钥 TOFU 校验（hostkey.go）：首连记录指纹、再连比对，不一致即拒绝。
		// 目标是平台自建、IP 由 DHCP 动态分配的短生命周期虚拟机，重建即换主机密钥，
		// 人工核对 known_hosts 不可操作，故采用自动 TOFU；范围另由 ValidateTarget 收敛。
		HostKeyCallback: TOFUHostKeyCallback(),
		Timeout:         8 * time.Second, // TCP 拨号超时（ssh.Dial 内部用它做 net.DialTimeout）
	}
}

// Dial 建立 SSH 连接——**全仓库唯一的 ssh.Dial 出口**（终端桥与 Run 一族都走这里）。
// opts 必须来自 NewOptions：未经校验的零值一律拒绝，杜绝「绕过白名单直接拨号」。
func Dial(opts Options) (*ssh.Client, error) {
	if !opts.validated {
		return nil, errors.New("SSH 连接参数未经安全校验（必须经 NewOptions 构造）")
	}
	return ssh.Dial("tcp", opts.Addr(), clientConfig(opts))
}

// RunWithStdin 在 Run 的基础上向命令的 stdin 喂入 stdin 内容（nil 表示不写 stdin），
// 供「cat > 路径」式的文件写入使用。命令非零退出返回 *ssh.ExitError，stderr 带远程报错原文。
func RunWithStdin(opts Options, cmd string, stdin io.Reader, timeout time.Duration) (stdout string, stderr string, err error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	client, err := Dial(opts)
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
					log.Printf("[vmssh] stdin 写入 panic host=%s:%d panic=%v\n%s", opts.host, opts.port, rec, debug.Stack())
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
