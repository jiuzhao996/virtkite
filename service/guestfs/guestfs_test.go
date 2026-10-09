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

// TestBuildCustomizeArgs 覆盖 customize 参数组装（--run 脚本路径 + 可选 --ssh-inject 公钥）。
func TestBuildCustomizeArgs(t *testing.T) {
	got := strings.Join(buildCustomizeArgs("/p/a.qcow2", "/tmp/opt.sh", ""), " ")
	if got != "-a /p/a.qcow2 --run /tmp/opt.sh" {
		t.Errorf("customize 参数不符: %q", got)
	}
	got = strings.Join(buildCustomizeArgs("/p/a.qcow2", "/tmp/opt.sh", "/k.pub"), " ")
	if got != "-a /p/a.qcow2 --run /tmp/opt.sh --ssh-inject root:file:/k.pub" {
		t.Errorf("公钥注入参数不符: %q", got)
	}
}

// TestBuildSparsifyArgs 覆盖 sparsify 参数组装（原地压缩）。
func TestBuildSparsifyArgs(t *testing.T) {
	got := strings.Join(buildSparsifyArgs("/p/a.qcow2"), " ")
	if got != "--in-place /p/a.qcow2" {
		t.Errorf("sparsify 参数不符: %q", got)
	}
}

// TestOptimizeScriptEmbedded 确认优化脚本组装正确：公共段 + 两族分支 + MOTD + 末尾 exit 0；
// 自定义段非空时被追加。
func TestOptimizeScriptEmbedded(t *testing.T) {
	s := OptimizeScript("")
	for _, want := range []string{
		"qemu-guest-agent",        // 公共/族段装 agent
		"cloud-init",              // 同上
		"if command -v dnf",       // RHEL 系分支
		"elif command -v apt-get", // Debian 系分支
		"00-vmops-motd.sh",        // MOTD 彩色欢迎语
		"cloud-utils-growpart",    // RHEL 包名
		"cloud-guest-utils",       // Debian 包名
		"exit 0",                  // 末尾必须 exit 0（否则 virt-customize 判失败）
	} {
		if !strings.Contains(s, want) {
			t.Errorf("优化脚本缺少 %q", want)
		}
	}
	withCustom := OptimizeScript("echo CUSTOM_MARKER")
	if !strings.Contains(withCustom, "CUSTOM_MARKER") {
		t.Error("自定义段未被追加")
	}
	if strings.Contains(OptimizeScript(""), "CUSTOM_MARKER") {
		t.Error("空自定义段不应产生内容")
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
