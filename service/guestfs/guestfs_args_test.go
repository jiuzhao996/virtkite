package guestfs

import (
	"reflect"
	"strings"
	"testing"
)

// TestBuildSysprepArgsEdge 补充 virt-sysprep 参数组装的边界。
//
// 风险点：disableOps 为空时绝不能不产出空 `--disable ""`（会被 virt-sysprep 当成
// 「禁用名为空串的操作」而报错）；参数须保持调用顺序，`--disable` 的值用逗号拼接。
func TestBuildSysprepArgsEdge(t *testing.T) {
	cases := []struct {
		name       string
		disk       string
		disableOps []string
		want       []string
	}{
		{"nil disableOps 不加 --disable", "/d.img", nil, []string{"-a", "/d.img"}},
		{"空切片不加 --disable", "/d.img", []string{}, []string{"-a", "/d.img"}},
		{"单项 --disable", "/d.img", []string{"logfiles"}, []string{"-a", "/d.img", "--disable", "logfiles"}},
		{
			"多项按调用顺序逗号拼接",
			"/d.img",
			[]string{"bash-history", "logfiles"},
			[]string{"-a", "/d.img", "--disable", "bash-history,logfiles"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := buildSysprepArgs(c.disk, c.disableOps); !reflect.DeepEqual(got, c.want) {
				t.Errorf("buildSysprepArgs=%q want %q", got, c.want)
			}
		})
	}
}

// TestBuildCustomizeArgsExact 逐元素校验参数数组。
//
// 风险点：这里必须用数组元素（而非 join）比对——公钥路径等参数可能含空格，
// 代码用 exec.CommandContext 数组传参、不经 shell，空格必须原样保留为**单个**参数，
// 一旦被拆成多段就会造成注入或路径错误。
func TestBuildCustomizeArgsExact(t *testing.T) {
	got := buildCustomizeArgs("/d.img", "/tmp/opt.sh", "")
	want := []string{"-a", "/d.img", "--run", "/tmp/opt.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("无公钥: got %q want %q", got, want)
	}

	got = buildCustomizeArgs("/d.img", "/tmp/opt.sh", "/k.pub")
	want = []string{"-a", "/d.img", "--run", "/tmp/opt.sh", "--ssh-inject", "root:file:/k.pub"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("有公钥: got %q want %q", got, want)
	}

	// 含空格的公钥路径仍是单个参数
	got = buildCustomizeArgs("/d.img", "/tmp/opt.sh", "/k with space.pub")
	if len(got) != 6 || got[5] != "root:file:/k with space.pub" {
		t.Errorf("含空格公钥应保持为单个参数: %q", got)
	}
}

// TestBuildSparsifyArgsExact 逐元素校验 sparsify 参数（原地压缩）。
func TestBuildSparsifyArgsExact(t *testing.T) {
	want := []string{"--in-place", "/d.img"}
	if got := buildSparsifyArgs("/d.img"); !reflect.DeepEqual(got, want) {
		t.Errorf("buildSparsifyArgs=%q want %q", got, want)
	}
}

// TestOptimizeScriptWhitespaceCustom 空白自定义段视同为空，不产生「自定义优化段」块。
// 风险点：用 TrimSpace 而非 len>0 判定，否则只含空白的 custom 会塞进一段空块。
func TestOptimizeScriptWhitespaceCustom(t *testing.T) {
	s := OptimizeScript("   \n\t \n")
	if strings.Contains(s, "自定义优化段") {
		t.Errorf("纯空白自定义段不应产生自定义块:\n%s", s)
	}
}

// TestOptimizeScriptTrailingNewlineIdempotent 自定义段带不带尾换行，成品脚本应完全一致。
//
// 风险点：OptimizeScript 有一段 `if !HasSuffix(custom,"\n") { 补一个换行 }` 的守卫，
// 目的是「统一尾换行」。此用例把该不变式钉死：两种输入产物必须逐字节相等。
func TestOptimizeScriptTrailingNewlineIdempotent(t *testing.T) {
	noNL := OptimizeScript("echo X")
	withNL := OptimizeScript("echo X\n")
	if noNL != withNL {
		t.Errorf("自定义段尾换行不应改变产物:\n无换行=%q\n有换行=%q", noNL, withNL)
	}
	if !strings.Contains(noNL, "echo X") {
		t.Errorf("自定义内容丢失:\n%s", noNL)
	}
}

// TestOptimizeScriptStructure 结构不变量：shebang 开头、set +e、发行版分支顺序
// （先 RHEL 系 dnf/yum，后 Debian 系 apt-get）、以 exit 0 收尾。
//
// 风险点：
//   - 缺少 shebang 或 `set +e` 会让 virt-customize 因中途命令失败而整体判失败；
//   - if/elif 分支顺序错乱会把 RHEL 机误判入 Debian 分支；
//   - 末尾必须 exit 0，否则 virt-customize 判 customize 失败（既有测试已守护该点）。
func TestOptimizeScriptStructure(t *testing.T) {
	s := OptimizeScript("")
	if !strings.HasPrefix(s, "#!/bin/sh\n") {
		t.Error("缺少 shebang 头")
	}
	if !strings.Contains(s, "set +e") {
		t.Error("缺少 set +e（best-effort 执行）")
	}
	if !strings.HasSuffix(s, "exit 0\n") {
		t.Error("应以 exit 0 收尾")
	}
	rhIdx := strings.Index(s, "command -v dnf")
	debIdx := strings.Index(s, "elif command -v apt-get")
	if rhIdx < 0 || debIdx < 0 || rhIdx >= debIdx {
		t.Errorf("发行版分支顺序错乱: dnf@%d apt-get@%d", rhIdx, debIdx)
	}
}
