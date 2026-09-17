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
	opts := Options{Host: "10.0.0.5"}
	if opts.Port != 0 {
		t.Fatalf("Port 零值应为 0（由 RunWithStdin 内部补 22），得到 %d", opts.Port)
	}
}
