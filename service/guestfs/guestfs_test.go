package guestfs

import (
	"strings"
	"testing"
)

// TestBuildSysprepArgs 覆盖 sysprep 参数组装：默认全量，disableOps 非空时排除指定项。
func TestBuildSysprepArgs(t *testing.T) {
	got := strings.Join(buildSysprepArgs("/p/a.qcow2", nil), " ")
	if got != "-a /p/a.qcow2" {
		t.Errorf("默认参数不符: %q", got)
	}
	got = strings.Join(buildSysprepArgs("/p/a.qcow2", []string{"bash-history", "logfiles"}), " ")
	if got != "-a /p/a.qcow2 --disable bash-history,logfiles" {
		t.Errorf("--disable 参数不符: %q", got)
	}
}

// TestBuildCustomizeArgs 覆盖 customize 参数组装（--run 脚本路径）。
func TestBuildCustomizeArgs(t *testing.T) {
	got := strings.Join(buildCustomizeArgs("/p/a.qcow2", "/tmp/opt.sh"), " ")
	if got != "-a /p/a.qcow2 --run /tmp/opt.sh" {
		t.Errorf("customize 参数不符: %q", got)
	}
}

// TestBuildSparsifyArgs 覆盖 sparsify 参数组装（原地压缩）。
func TestBuildSparsifyArgs(t *testing.T) {
	got := strings.Join(buildSparsifyArgs("/p/a.qcow2"), " ")
	if got != "--in-place /p/a.qcow2" {
		t.Errorf("sparsify 参数不符: %q", got)
	}
}

// TestOptimizeScriptEmbedded 确认内置优化脚本已随包嵌入且内容合理（装 agent + 末尾 exit 0）。
func TestOptimizeScriptEmbedded(t *testing.T) {
	s := OptimizeScript()
	if !strings.Contains(s, "qemu-guest-agent") {
		t.Error("内置优化脚本应安装 qemu-guest-agent")
	}
	if !strings.Contains(s, "cloud-init") {
		t.Error("内置优化脚本应安装 cloud-init")
	}
	if !strings.Contains(s, "exit 0") {
		t.Error("内置优化脚本必须以 exit 0 结尾（否则 virt-customize 判失败）")
	}
}

// TestTailLines 覆盖失败时取末尾行的截断逻辑。
func TestTailLines(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f"}
	if got := tailLines(lines, 3); got != "d\ne\nf" {
		t.Errorf("tailLines(6,3) = %q", got)
	}
	if got := tailLines([]string{"x", "y"}, 5); got != "x\ny" {
		t.Errorf("行数不足应全返回: %q", got)
	}
	if got := tailLines(nil, 3); got != "" {
		t.Errorf("空输入应返回空串: %q", got)
	}
}
