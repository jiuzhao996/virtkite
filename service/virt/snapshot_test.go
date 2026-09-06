package virt

import (
	"encoding/xml"
	"fmt"
	"strings"
	"testing"

	"github.com/digitalocean/go-libvirt"
)

// TestXMLEscape 覆盖 XML 五个元字符与几类容易被忽略的输入。
//
// 风险点：xmlEscape 是快照 XML 唯一的注入防线。CreateSnapshot 用 fmt.Sprintf
// 拼 <domainsnapshot> 模板（snapshot.go:149），快照名与描述都来自前端可控输入，
// 不转义就能闭合 <name> 标签往 domainsnapshot 里塞任意节点。
// 这里同时锁死「转义成什么形式」——xml.EscapeText 输出的是 &#34;/&#39; 而不是
// &quot;/&apos;，将来有人改成手写 strings.NewReplacer 很容易只换 &<> 三个，
// 或者换成 HTML 实体名，用例会立刻抓到。
func TestXMLEscape(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"与号 &（必须最先被处理，否则会二次转义）", "a&b", "a&amp;b"},
		{"小于号 <（闭合标签注入的关键字符）", "a<b", "a&lt;b"},
		{"大于号 >", "a>b", "a&gt;b"},
		{"双引号 \"（转成数字实体 &#34; 而非 &quot;）", `a"b`, "a&#34;b"},
		{"单引号 '（转成数字实体 &#39; 而非 &apos;）", "a'b", "a&#39;b"},
		{"五个元字符同时出现", `&<>"'`, "&amp;&lt;&gt;&#34;&#39;"},
		{"空串", "", ""},
		{"纯中文（合法 UTF-8，不应被转义）", "生产环境快照", "生产环境快照"},
		{"中文混元字符", "备份<主库>&从库", "备份&lt;主库&gt;&amp;从库"},
		{"普通快照名（无元字符时原样返回）", "snap-2024-06-01", "snap-2024-06-01"},
		{"换行符（转成 &#xA;，避免破坏单行模板）", "a\nb", "a&#xA;b"},
		{"制表符（转成 &#x9;）", "a\tb", "a&#x9;b"},
		{"回车符（转成 &#xD;）", "a\rb", "a&#xD;b"},
		// xml.EscapeText 处理的是「字面文本」，输入里的 &amp; 是五个普通字符，
		// 转义后必然变成 &amp;amp;。这不是 bug：对文本转义器来说幂等才是错的
		// （否则用户真想要一个字面量 "&amp;" 就永远表达不出来）。
		// 用例把这个语义固定下来，防止有人误以为「转义两次是 bug」而加上
		// 「已转义就跳过」的判断——那种判断会直接打开注入口子。
		{"已转义串会被再次转义（文本转义器的正确语义，非幂等）", "&amp;", "&amp;amp;"},
		{"已转义的 &lt; 同样会被再次转义", "&lt;", "&amp;lt;"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := xmlEscape(tt.in)
			if got != tt.want {
				t.Errorf("xmlEscape(%q) 期望 %q，实际 %q", tt.in, tt.want, got)
			}
		})
	}
}

// TestXMLEscapeBlocksSnapshotXMLInjection 用真实注入载荷验证快照 XML 不会被撑破。
//
// 风险点：只断言「元字符被替换」还不足以证明安全，必须证明拼出来的整段
// domainsnapshot XML 解析后仍然只有一个 <name>、一个 <description>，
// 且内容等于用户输入的原文。载荷模仿真实攻击：闭合 <name> 后追加
// <memory snapshot='external'/> 这类会改变快照语义的节点。
//
// 注意：模板字符串是从 snapshot.go 的 CreateSnapshot 镜像过来的
// （CreateSnapshot 本身需要 libvirt 连接，纯函数测试无法直接调用）。
// 模板一旦在生产代码里改动，这里也要同步。
func TestXMLEscapeBlocksSnapshotXMLInjection(t *testing.T) {
	tests := []struct {
		name    string
		snapNam string
		desc    string
	}{
		{
			name:    "快照名闭合标签后追加 memory 节点",
			snapNam: `snap</name><memory snapshot='external' file='/etc/shadow'/><name>tail`,
			desc:    "正常描述",
		},
		{
			name:    "描述里注入整段 disks 节点",
			snapNam: "snap-ok",
			desc:    `x</description><disks><disk name='vda' snapshot='no'/></disks><description>y`,
		},
		{
			name:    "名称与描述同时注入，且混入中文",
			snapNam: `<坏名字 & "引号">`,
			desc:    `</description></domainsnapshot><domainsnapshot><name>覆盖</name><description>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 与 snapshot.go CreateSnapshot 完全相同的模板
			got := fmt.Sprintf(`<domainsnapshot>
  <name>%s</name>
  <description>%s</description>
</domainsnapshot>`, xmlEscape(tt.snapNam), xmlEscape(tt.desc))

			var parsed struct {
				XMLName xml.Name  `xml:"domainsnapshot"`
				Names   []string  `xml:"name"`
				Descs   []string  `xml:"description"`
				Memory  *struct{} `xml:"memory"`
				Disks   *struct{} `xml:"disks"`
			}
			if err := xml.Unmarshal([]byte(got), &parsed); err != nil {
				t.Fatalf("转义后的快照 XML 应当仍是合法 XML，解析失败: %v\nXML: %s", err, got)
			}
			if len(parsed.Names) != 1 {
				t.Fatalf("<name> 节点数期望 1，实际 %d（说明注入成功闭合了标签）\nXML: %s", len(parsed.Names), got)
			}
			if len(parsed.Descs) != 1 {
				t.Fatalf("<description> 节点数期望 1，实际 %d\nXML: %s", len(parsed.Descs), got)
			}
			if parsed.Names[0] != tt.snapNam {
				t.Errorf("<name> 内容期望还原为原文 %q，实际 %q", tt.snapNam, parsed.Names[0])
			}
			if parsed.Descs[0] != tt.desc {
				t.Errorf("<description> 内容期望还原为原文 %q，实际 %q", tt.desc, parsed.Descs[0])
			}
			if parsed.Memory != nil {
				t.Error("注入的 <memory> 节点出现在快照 XML 中，转义失效")
			}
			if parsed.Disks != nil {
				t.Error("注入的 <disks> 节点出现在快照 XML 中，转义失效")
			}
			// 载荷里的裸标签不应以可执行形式残留
			if strings.Contains(got, "<memory") || strings.Contains(got, "<disks") {
				t.Errorf("XML 中残留未转义的注入标签: %s", got)
			}
		})
	}
}

// TestSnapshotStateToInt32 覆盖快照 XML <state> 的字符串态、数字态与非法输入。
//
// 风险点：libvirt 各版本对快照 <state> 的输出并不统一——多数版本给状态名
// （running/shutoff），也有实现直接给枚举数字，还有 disk-only 快照会给
// 平台完全不认识的取值。snapshotInfo 依赖 ok 返回值决定是否降级成 "error"，
// 因此「非法输入必须返回 false」和「合法输入必须给出正确枚举」同等重要：
// 前者判错会把 0（nostate）当成有效状态，后者判错会让快照列表状态全错。
func TestSnapshotStateToInt32(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   int32
		wantOK bool
	}{
		{"nostate", "nostate", int32(libvirt.DomainNostate), true},
		{"running", "running", int32(libvirt.DomainRunning), true},
		{"blocked", "blocked", int32(libvirt.DomainBlocked), true},
		{"paused", "paused", int32(libvirt.DomainPaused), true},
		{"shutdown", "shutdown", int32(libvirt.DomainShutdown), true},
		{"shutoff", "shutoff", int32(libvirt.DomainShutoff), true},
		{"crashed", "crashed", int32(libvirt.DomainCrashed), true},
		{"pmsuspended", "pmsuspended", int32(libvirt.DomainPmsuspended), true},
		{"带首尾空白（XML 缩进产生）", "  running  ", int32(libvirt.DomainRunning), true},
		{"带换行（多行 XML 产生）", "\nshutoff\n", int32(libvirt.DomainShutoff), true},
		{"数字形式 5（部分 libvirt 实现直接输出枚举）", "5", int32(libvirt.DomainShutoff), true},
		{"数字形式 0", "0", 0, true},
		{"空串（<state> 缺失）", "", 0, false},
		{"纯空白（等价于缺失）", "   ", 0, false},
		{"disk-snapshot 特有的 disk-snapshot 取值", "disk-snapshot", 0, false},
		{"未知状态名", "hibernated", 0, false},
		{"大写 RUNNING（switch 大小写敏感，不识别）", "RUNNING", 0, false},
		{"首字母大写 Running", "Running", 0, false},
		{"int32 溢出的数字（ParseInt 失败后落到 switch）", "99999999999", 0, false},
		{"小数", "1.5", 0, false},
		// 负数与越界数字能通过 ParseInt，函数照原样返回 true；
		// 下游 StateToPlatform 会把它们兜成 error，因此不构成故障。
		{"负数 -1（ParseInt 成功，交给 StateToPlatform 兜底）", "-1", -1, true},
		{"越界数字 42（ParseInt 成功，交给 StateToPlatform 兜底）", "42", 42, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := snapshotStateToInt32(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("snapshotStateToInt32(%q) 的 ok 期望 %v，实际 %v", tt.in, tt.wantOK, ok)
			}
			if got != tt.want {
				t.Errorf("snapshotStateToInt32(%q) 期望枚举值 %d，实际 %d", tt.in, tt.want, got)
			}
		})
	}
}

// TestSnapshotStateToPlatformChain 验证「快照 <state> → 枚举 → 平台状态」整条链路。
//
// 风险点：snapshotInfo 的真实逻辑是两个函数串起来的
// （snapshotStateToInt32 拿枚举，StateToPlatform 转平台状态，失败则降级 "error"）。
// 单独测两端都通过、串起来仍可能出错——例如 snapshotStateToInt32 把
// "shutdown" 映射到 DomainShutoff 而不是 DomainShutdown，单测两端都看不出问题，
// 但快照列表里「正在关机」与「已关机」会混淆。这条用例按前端实际看到的
// 字符串来断言，是最贴近用户的一层。
func TestSnapshotStateToPlatformChain(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"running → running", "running", StatusRunning},
		{"shutoff → shut off", "shutoff", StatusShutOff},
		{"shutdown → shut off", "shutdown", StatusShutOff},
		{"paused → paused", "paused", StatusPaused},
		{"crashed → error", "crashed", StatusError},
		{"blocked → error", "blocked", StatusError},
		{"pmsuspended → error", "pmsuspended", StatusError},
		{"nostate → error", "nostate", StatusError},
		{"无法识别时降级 error", "disk-snapshot", StatusError},
		{"<state> 缺失时降级 error", "", StatusError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 复刻 snapshotInfo 的降级逻辑：识别失败即 "error"
			got := StatusError
			if st, ok := snapshotStateToInt32(tt.in); ok {
				got = StateToPlatform(st)
			}
			if got != tt.want {
				t.Errorf("快照 state=%q 期望映射为平台状态 %q，实际 %q", tt.in, tt.want, got)
			}
			if !platformStatuses[got] {
				t.Errorf("快照 state=%q 映射出平台不认识的状态 %q", tt.in, got)
			}
		})
	}
}
