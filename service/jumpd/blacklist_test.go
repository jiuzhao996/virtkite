package jumpd

import (
	"strings"
	"testing"
)

// TestParseBlacklist 逗号分隔解析 + 空值回退内置默认
func TestParseBlacklist(t *testing.T) {
	items := ParseBlacklist(" rm -rf , MKFS , dd if= ,, ")
	if len(items) != 3 || items[0] != "rm -rf" || items[1] != "mkfs" || items[2] != "dd if=" {
		t.Fatalf("解析/归一不对: %v", items)
	}
	fallback := ParseBlacklist("")
	if len(fallback) == 0 {
		t.Fatalf("空值应回退内置默认")
	}
	if len(fallback) != len(defaultCmdBlacklist) {
		t.Fatalf("回退应等于内置默认")
	}
}

// TestMatchBlacklist 归一化子串匹配：大小写/多空格变体都拦得住；普通命令放行
func TestMatchBlacklist(t *testing.T) {
	entries := defaultCmdBlacklist
	cases := []struct {
		line string
		want bool
	}{
		{"rm -rf /", true},
		{"RM  -RF /data", true},       // 大小写 + 多空格
		{"cd /var && rm -rf ./cache", true},
		{"mkfs.ext4 /dev/vdb", true},  // mkfs 子串
		{"dd if=/dev/zero of=/dev/sda", true},
		{"ls -la", false},
		{"systemctl restart nginx", false},
		{"echo rm -rf", true}, // 含子串即拦（教学场景宁严勿漏）
		{"", false},
	}
	for _, c := range cases {
		if got := matchBlacklist(c.line, entries); got != c.want {
			t.Fatalf("matchBlacklist(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

// TestNormalizeCmdLine 归一化细节
func TestNormalizeCmdLine(t *testing.T) {
	if normalizeCmdLine("  RM \t -RF ") != "rm -rf" {
		t.Fatalf("归一化结果不对: %q", normalizeCmdLine("  RM \t -RF "))
	}
	if strings.Contains(normalizeCmdLine("A   B"), "  ") {
		t.Fatalf("连续空白应折叠")
	}
}
