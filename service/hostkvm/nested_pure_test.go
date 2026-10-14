package hostkvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShellQuote 单引号包裹 + 内部单引号转义，绝不产生可越出引号边界的字符。
//
// 风险点：shellQuote 的产物会被拼进「以 root 经 sudo 执行、写 /etc/modprobe.d」的
// shell 命令里。若它对单引号/换行/$(...)/反引号处理不当，就是一条 root 命令注入
// （内容里含 `'` 即可闭合引号，后续任意命令以 root 执行）。本用例不执行任何命令，
// 只验证纯字符串变换：固定期望 + 对一批含特殊字符输入的「反转义往返」。
func TestShellQuote(t *testing.T) {
	fixed := []struct {
		name, in, want string
	}{
		{"普通串", "hello", "'hello'"},
		{"空串", "", "''"},
		{"单个单引号", "it's", `'it'\''s'`},
		{"分号命令注入尝试", "a; rm -rf /", "'a; rm -rf /'"},
	}
	for _, tc := range fixed {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellQuote(tc.in); got != tc.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// 往返：用只认识「单引号段 + \' 转义」的解析器还原，必须与原文一字不差。
	// 覆盖注入常用的元字符：单引号、反引号、$()、分号、管道、换行、反斜杠、双引号。
	tricky := []string{
		"simple",
		"it's",
		"a'b'c",
		"'",
		"a'",
		"'a",
		"line1\nline2",
		"`whoami`",
		"$(id)",
		"a; rm -rf /; b",
		"left && right || x",
		"pipe|cat",
		`back\slash`,
		`double"quote`,
		"中文内容与换行\n结束",
		"options kvm_intel nested=1\n# 注释\n",
	}
	for _, in := range tricky {
		quoted := shellQuote(in)
		if !strings.HasPrefix(quoted, "'") || !strings.HasSuffix(quoted, "'") {
			t.Errorf("shellQuote(%q) = %q 未以单引号包裹", in, quoted)
		}
		if got := unquoteShell(t, quoted); got != in {
			t.Errorf("shellQuote 往返不一致: in=%q, out=%q（转义有误，命令注入风险）", in, got)
		}
	}
}

// unquoteShell 解析 shellQuote 的产物（单引号段 + \' 转义）还原原文，仅供测试。
func unquoteShell(t *testing.T, s string) string {
	t.Helper()
	if len(s) == 0 || s[0] != '\'' {
		t.Fatalf("非法 shell 引号串（应以 ' 开头）: %q", s)
	}
	var b strings.Builder
	i := 1 // 跳过开引号
	for i < len(s) {
		if s[i] == '\'' {
			// '\''（开引号 + 反斜杠 + 单引号 + 引号）表示一个字面单引号
			if strings.HasPrefix(s[i:], `'\''`) {
				b.WriteByte('\'')
				i += 4
				continue
			}
			// 否则是收尾引号，且必须位于末尾
			if i != len(s)-1 {
				t.Fatalf("引号串内部出现未转义的收尾引号 at %d: %q", i, s)
			}
			return b.String()
		}
		b.WriteByte(s[i])
		i++
	}
	t.Fatalf("shell 引号串未闭合: %q", s)
	return ""
}

// TestDirExists 仅「存在且确为目录」返回 true。
//
// 风险点：Detect 用它判断 /sys/module/kvm_intel|kvm_amd 是否存在。写成只判 os.Stat==nil
// 会把普通文件也当模块目录，导致误报「支持嵌套虚拟化」。用例覆盖：真目录、缺失路径、
// 同名的普通文件三种。
func TestDirExists(t *testing.T) {
	t.Run("现存目录", func(t *testing.T) {
		if !dirExists(t.TempDir()) {
			t.Error("现存目录应返回 true")
		}
	})
	t.Run("不存在路径", func(t *testing.T) {
		if dirExists(filepath.Join(t.TempDir(), "nope")) {
			t.Error("不存在路径应返回 false")
		}
	})
	t.Run("同名普通文件", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "afile")
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if dirExists(f) {
			t.Error("普通文件应返回 false（必须判 IsDir）")
		}
	})
}

// TestNestedConfPathConstant 锁死持久化配置的落盘路径。
//
// 风险点：该路径是 Set() 以 root 经 sudo 覆写的目标。路径若被改错，会覆盖宿主机上
// 无关的 /etc/modprobe.d 配置（影响其它内核模块行为）。用例把常量钉死，任何改动都需显式改这里。
func TestNestedConfPathConstant(t *testing.T) {
	if nestedConfPath != "/etc/modprobe.d/vmops-kvm-nested.conf" {
		t.Errorf("nestedConfPath = %q, want %q", nestedConfPath, "/etc/modprobe.d/vmops-kvm-nested.conf")
	}
	if !strings.HasPrefix(nestedConfPath, "/etc/modprobe.d/") {
		t.Errorf("nestedConfPath 必须落在 /etc/modprobe.d/ 下，实际 %q", nestedConfPath)
	}
}

// TestDetectInvariants 断言 Detect 返回值自洽（只读 /sys，无副作用）。
//
// 风险点：Detect 读 /sys/module/<mod>/parameters/nested 解析状态。字段间的自洽关系
// （Supported⇒有模块名、Enabled⇒原始值为真值、Value 非空⇒是内核真值域）是把解析结果
// 呈现给设置页与 Set() 决策的基础；不一致会让前端显示与真实内核状态相反。
// 注意：本用例刻意不调用 Set()——它会写 /etc/modprobe.d 并重载内核模块，绝不在测试中触发。
func TestDetectInvariants(t *testing.T) {
	st := Detect()
	if st == nil {
		t.Fatal("Detect 不应返回 nil")
	}

	if st.Supported {
		if st.Module != "kvm_intel" && st.Module != "kvm_amd" {
			t.Errorf("Supported 时 Module 必须是 kvm_intel/kvm_amd，实际 %q", st.Module)
		}
	} else {
		if st.Module != "" {
			t.Errorf("不支持时 Module 应为空，实际 %q", st.Module)
		}
		if st.Reason == "" {
			t.Error("不支持时必须给出原因")
		}
	}

	if st.Enabled && st.Value == "" {
		t.Error("Enabled=true 时应有原始值")
	}
	if st.Value != "" {
		switch st.Value {
		case "Y", "y", "1", "N", "n", "0":
			// 内核 nested 参数的真值域
		default:
			t.Errorf("Value 落在外核真值域之外: %q", st.Value)
		}
		// Enabled 必须与原始值一致（真值域：Y/y/1 为开）
		wantEnabled := st.Value == "Y" || st.Value == "y" || st.Value == "1"
		if st.Enabled != wantEnabled {
			t.Errorf("Enabled=%v 与 Value=%q 不一致", st.Enabled, st.Value)
		}
	}

	// 幂等：多次探测结果一致（确认无隐藏副作用/状态漂移）
	if again := Detect(); again.Supported != st.Supported || again.Module != st.Module || again.Value != st.Value {
		t.Errorf("Detect 非幂等: %+v vs %+v", again, st)
	}
}
