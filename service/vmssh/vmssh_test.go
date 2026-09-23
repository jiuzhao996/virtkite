package vmssh

import (
	"strings"
	"testing"
	"time"
)

// TestShellQuote 单引号包裹与内部单引号转义（路径等不可信输入拼进远程命令前的最后一道防线）。
func TestShellQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"普通绝对路径", "/root/a.txt", "'/root/a.txt'"},
		{"空串", "", "''"},
		{"含空格", "/root/my dir/f 1.txt", "'/root/my dir/f 1.txt'"},
		{"内部单引号", "/root/it's.txt", "'/root/it'\\''s.txt'"},
		{"连续单引号", "a''b", "'a'\\'''\\''b'"},
		{"命令替换注入", "$(rm -rf /)", "'$(rm -rf /)'"},
		{"反引号注入", "`id`", "'`id`'"},
		{"分号注入", "/a; cat /etc/shadow", "'/a; cat /etc/shadow'"},
		{"管道注入", "/a | shutdown", "'/a | shutdown'"},
		{"换行符", "/root/a\nb", "'/root/a\nb'"},
		{"反斜杠与变量", `/tmp\$HOME\x`, "'/tmp\\$HOME\\x'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShellQuote(tc.in)
			if got != tc.want {
				t.Fatalf("ShellQuote(%q) = %q, 期望 %q", tc.in, got, tc.want)
			}
			// 不变量：结果必须整体被单引号包裹（内部 $ ` ; 等元字符因此全部失去特殊含义）
			if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
				t.Fatalf("结果 %q 未被单引号整体包裹", got)
			}
		})
	}
}

// TestDefaultOptions 默认值约定：端口缺省 22、超时缺省 DefaultTimeout（15s）。
func TestDefaultOptions(t *testing.T) {
	if DefaultTimeout != 15*time.Second {
		t.Fatalf("DefaultTimeout = %s, 期望 15s", DefaultTimeout)
	}
	// 端口缺省 22 现在由 NewOptions 补齐（Options 不可字面量构造，唯一出口即校验口）
	opts, err := NewOptions("", "10.0.0.5", 0, "root", "pwd")
	if err != nil {
		t.Fatalf("私有网段目标应通过校验，实际报错: %v", err)
	}
	if opts.Port() != 22 {
		t.Fatalf("端口缺省应为 22，得到 %d", opts.Port())
	}
	if opts.Host() != "10.0.0.5" || opts.User() != "root" {
		t.Fatalf("访问器回读不一致: host=%q user=%q", opts.Host(), opts.User())
	}
	if opts.Addr() != "10.0.0.5:22" {
		t.Fatalf("Addr 应为 10.0.0.5:22，得到 %q", opts.Addr())
	}
}

// TestUnvalidatedOptionsRejected 拨号入口必须拒绝未经 NewOptions 的零值参数：
// 这是「绕过白名单直接外连」的最后一道兜底。
func TestUnvalidatedOptionsRejected(t *testing.T) {
	var zero Options
	if _, err := Dial(zero); err == nil {
		t.Fatal("零值 Options 必须被 Dial 拒绝，否则白名单可被绕过")
	}
	if _, _, err := Run(zero, "echo hi", time.Second); err == nil {
		t.Fatal("零值 Options 必须被 Run 拒绝，否则白名单可被绕过")
	}
}

// TestNewOptionsRejectsPublicTarget 公网地址不得作为拨号目标（防平台变跳板机）。
func TestNewOptionsRejectsPublicTarget(t *testing.T) {
	for _, host := range []string{"8.8.8.8", "203.0.113.9", "example.com", "127.0.0.1", "::1"} {
		if _, err := NewOptions("", host, 22, "root", "pwd"); err == nil {
			t.Fatalf("目标 %q 应被白名单拒绝（公网/主机名/环回），实际放行", host)
		}
	}
}
