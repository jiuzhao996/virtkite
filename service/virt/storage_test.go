package virt

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// xmlElementCounts 遍历 XML token 流，统计「从根开始的元素路径」各出现多少次。
// 返回 key 形如 volume、volume/target、volume/target/format。
//
// 为什么不用「定义带切片字段的结构体再看 len」：那样只能数出自己预先声明过的
// 元素名，注入进来一个结构体里没有的元素（比如 <permissions>、<encryption>）
// 会被 encoding/xml 静默丢弃，测试反而看不见。token 遍历是全量的，
// 任何位置多出来一个节点都会体现在计数里——这正是注入专项需要的证据。
func xmlElementCounts(t *testing.T, doc string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	var stack []string
	dec := xml.NewDecoder(strings.NewReader(doc))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("XML 应当可被解析，token 遍历失败: %v\nXML: %s", err, doc)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			stack = append(stack, se.Name.Local)
			counts[strings.Join(stack, "/")]++
		case xml.EndElement:
			if len(stack) == 0 {
				t.Fatalf("XML 结构错乱：多出一个 </%s>\nXML: %s", se.Name.Local, doc)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 {
		t.Fatalf("XML 结构错乱：标签未闭合 %v\nXML: %s", stack, doc)
	}
	return counts
}

// assertXMLEscaped 注入专项的通用取证步骤，做两件事：
//
//  1. 输入里出现过的每个 XML 元字符，输出里必须能找到对应实体
//     （证明确实发生了转义，而不是字符被吞掉或截断）；
//  2. 输出里 '<' 与 '>' 的字面量个数必须恰好等于「元素个数 × 2」。
//     encoding/xml 把正文与属性里的 < > 全部转成 &lt; &gt;，
//     所以字面量的尖括号只可能来自合法标签的开闭；一旦注入成功多出一个标签，
//     计数立刻对不上。这一条比「输出里不含 </name> 这种子串」靠得住得多——
//     后者会把元素自己的合法闭合标签也算进去，永远为真地报错。
func assertXMLEscaped(t *testing.T, doc string, elementCount int, inputs ...string) {
	t.Helper()

	entities := []struct{ ch, ent, desc string }{
		{"&", "&amp;", "与号"},
		{"<", "&lt;", "小于号"},
		{">", "&gt;", "大于号"},
		{`"`, "&#34;", "双引号"},
		{"'", "&#39;", "单引号"},
		{"\t", "&#x9;", "制表符"},
		{"\n", "&#xA;", "换行符"},
		{"\r", "&#xD;", "回车符"},
	}
	joined := strings.Join(inputs, "")
	for _, e := range entities {
		if strings.Contains(joined, e.ch) && !strings.Contains(doc, e.ent) {
			t.Errorf("输入含%s %q，但输出里找不到实体 %q，转义失效\nXML: %s", e.desc, e.ch, e.ent, doc)
		}
	}

	if got, want := strings.Count(doc, "<"), elementCount*2; got != want {
		t.Errorf("输出里 '<' 字面量个数期望 %d（%d 个元素 × 开闭标签各一），实际 %d；多出来的只可能是注入的标签\nXML: %s",
			want, elementCount, got, doc)
	}
	if got, want := strings.Count(doc, ">"), elementCount*2; got != want {
		t.Errorf("输出里 '>' 字面量个数期望 %d，实际 %d\nXML: %s", want, got, doc)
	}
}

// parsedVolumeXML 用于反解 buildVolumeXML 的输出。
// 所有可能被注入的位置都声明成切片，好数个数——注入成功的表现就是某个切片长度 > 1。
type parsedVolumeXML struct {
	XMLName  xml.Name `xml:"volume"`
	Names    []string `xml:"name"`
	Capacity []struct {
		Unit string `xml:"unit,attr"`
		// 容量用字符串接，能原样看到写进 XML 的文本（"0"、"-1" 这类值不会被静默变形）
		Value string `xml:",chardata"`
	} `xml:"capacity"`
	Targets []struct {
		Formats []struct {
			Type string `xml:"type,attr"`
		} `xml:"format"`
		Paths []string `xml:"path"`
	} `xml:"target"`
	BackingStores []struct {
		Paths   []string `xml:"path"`
		Formats []struct {
			Type string `xml:"type,attr"`
		} `xml:"format"`
	} `xml:"backingStore"`
}

// parsedPoolXML 用于反解 buildDirPoolXML 的输出，同样用切片以便数节点。
type parsedPoolXML struct {
	XMLName xml.Name `xml:"pool"`
	Type    string   `xml:"type,attr"`
	Names   []string `xml:"name"`
	Targets []struct {
		Paths []string `xml:"path"`
	} `xml:"target"`
}

// unmarshalVolumeXML 反解卷 XML，失败即 Fatal（生成的 XML 必须始终合法）。
func unmarshalVolumeXML(t *testing.T, doc string) parsedVolumeXML {
	t.Helper()
	var v parsedVolumeXML
	if err := xml.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("生成的卷 XML 应当合法，解析失败: %v\nXML: %s", err, doc)
	}
	return v
}

// unmarshalPoolXML 反解池 XML，失败即 Fatal。
func unmarshalPoolXML(t *testing.T, doc string) parsedPoolXML {
	t.Helper()
	var p parsedPoolXML
	if err := xml.Unmarshal([]byte(doc), &p); err != nil {
		t.Fatalf("生成的池 XML 应当合法，解析失败: %v\nXML: %s", err, doc)
	}
	return p
}

// TestBuildVolumeXMLStructure 锁定 <volume> 的骨架：容量单位、卷格式、无 backingStore。
//
// 风险点集中在 capacity 的 unit 属性上。同一个 buildVolumeXML 被两条路径调用，
// 单位含义完全不同：CreateVolume/CreateVolumeCustom 传 "G"（数值是 GB），
// CloneVolumeFromVol 传 "B"（数值是从父卷读来的字节数）。
// 两者一旦串了，20GB 会变成 20 字节（虚拟机装不下系统盘，qemu 启动即失败），
// 或者 21474836480 会被当成 21474836480 GB（libvirt 直接拒绝创建）。
// 单位和数值又分处两个字段、类型都对得上，编译器帮不了忙，只能靠用例钉死。
// 另一半风险是 <backingStore>：普通建卷绝不能带它（带了就变成依赖某个父盘的增量卷）。
func TestBuildVolumeXMLStructure(t *testing.T) {
	tests := []struct {
		name     string
		volName  string
		format   string
		unit     string
		capacity int64
		wantUnit string
		wantCap  string
		wantFmt  string
	}{
		{
			name:    "常规 20G qcow2（CreateVolume 路径）",
			volName: "web-01.qcow2", format: volFormatQcow2, unit: volUnitGiB, capacity: 20,
			wantUnit: "G", wantCap: "20", wantFmt: "qcow2",
		},
		{
			name:    "raw 格式 10G（CreateVolumeCustom 支持 raw）",
			volName: "data.raw", format: "raw", unit: volUnitGiB, capacity: 10,
			wantUnit: "G", wantCap: "10", wantFmt: "raw",
		},
		{
			name:    "按字节声明 20GiB（CloneVolumeFromVol 路径，单位必须是 B）",
			volName: "clone.qcow2", format: volFormatQcow2, unit: volUnitByte, capacity: 21474836480,
			wantUnit: "B", wantCap: "21474836480", wantFmt: "qcow2",
		},
		{
			name:    "容量 0（handler 兜底前的极端值，生成侧原样输出不报错）",
			volName: "zero.qcow2", format: volFormatQcow2, unit: volUnitGiB, capacity: 0,
			wantUnit: "G", wantCap: "0", wantFmt: "qcow2",
		},
		{
			name:    "容量为负（生成侧不校验，交由 libvirt 拒绝）",
			volName: "neg.qcow2", format: volFormatQcow2, unit: volUnitGiB, capacity: -1,
			// TODO(疑似缺陷): buildVolumeXML 不校验 capacity。当前唯一的防线在
			//  handler/storage.go（Capacity <= 0 时兜成 20），virt 层自身没有兜底。
			//  若将来新增一条不经 handler 的调用路径（比如内部任务直接调 virt），
			//  就会把 <capacity unit="G">-1</capacity> 送进 libvirt。
			//  纵深防御的做法是在这里就拒绝 capacity <= 0。
			wantUnit: "G", wantCap: "-1", wantFmt: "qcow2",
		},
		{
			name:    "中文卷名（合法 UTF-8，不应被转义或截断）",
			volName: "系统盘.qcow2", format: volFormatQcow2, unit: volUnitGiB, capacity: 40,
			wantUnit: "G", wantCap: "40", wantFmt: "qcow2",
		},
		{
			name:    "空卷名与空格式（生成侧原样输出空元素）",
			volName: "", format: "", unit: volUnitGiB, capacity: 1,
			wantUnit: "G", wantCap: "1", wantFmt: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildVolumeXML(tt.volName, tt.format, tt.unit, tt.capacity)
			if err != nil {
				t.Fatalf("生成卷 XML 失败: %v", err)
			}

			v := unmarshalVolumeXML(t, got)
			if len(v.Names) != 1 {
				t.Fatalf("<name> 节点数期望 1，实际 %d\nXML: %s", len(v.Names), got)
			}
			if v.Names[0] != tt.volName {
				t.Errorf("卷名期望 %q，实际 %q", tt.volName, v.Names[0])
			}
			if len(v.Capacity) != 1 {
				t.Fatalf("<capacity> 节点数期望 1，实际 %d\nXML: %s", len(v.Capacity), got)
			}
			if v.Capacity[0].Unit != tt.wantUnit {
				t.Errorf("capacity 的 unit 属性期望 %q，实际 %q（单位串了会导致容量差 10 亿倍）",
					tt.wantUnit, v.Capacity[0].Unit)
			}
			if v.Capacity[0].Value != tt.wantCap {
				t.Errorf("capacity 数值期望 %q，实际 %q", tt.wantCap, v.Capacity[0].Value)
			}
			if len(v.Targets) != 1 || len(v.Targets[0].Formats) != 1 {
				t.Fatalf("期望恰好 1 个 <target> 且其下 1 个 <format>，实际 target=%d\nXML: %s",
					len(v.Targets), got)
			}
			if v.Targets[0].Formats[0].Type != tt.wantFmt {
				t.Errorf("target/format 的 type 属性期望 %q，实际 %q", tt.wantFmt, v.Targets[0].Formats[0].Type)
			}
			// 普通建卷绝不能带 backingStore：带了就变成某个父盘的增量子卷，
			// 父盘一删，这块盘直接不可读。
			if len(v.BackingStores) != 0 {
				t.Errorf("<backingStore> 节点数期望 0（buildVolumeXML 是非增量路径），实际 %d\nXML: %s",
					len(v.BackingStores), got)
			}
			if strings.Contains(got, "backingStore") {
				t.Errorf("XML 中不应出现 backingStore 字样，实际: %s", got)
			}
		})
	}
}

// TestBuildVolumeXMLExactWireFormat 钉住送给 libvirt 的确切字符串形态。
//
// 风险点：libvirt 对 <format type="qcow2"/> 与 <format type="qcow2"></format>
// 都接受，属性用单引号还是双引号也都接受，所以这类差异不会立刻报错。
// 但项目文档、docs/ 里的示例 XML 以及答辩演示脚本都是照抄这份输出的；
// 若有人把 encoding/xml 换回字符串拼接（那会重新打开注入口子），
// 输出形态一定变化，这个用例会第一时间发现，而结构化断言可能全都还过。
func TestBuildVolumeXMLExactWireFormat(t *testing.T) {
	tests := []struct {
		name string
		got  func() (string, error)
		want string
	}{
		{
			name: "无 backing 的普通卷",
			got: func() (string, error) {
				return buildVolumeXML("web-01.qcow2", volFormatQcow2, volUnitGiB, 20)
			},
			want: `<volume><name>web-01.qcow2</name><capacity unit="G">20</capacity>` +
				`<target><format type="qcow2"></format></target></volume>`,
		},
		{
			name: "带 backingStore 的增量子卷",
			got: func() (string, error) {
				return buildVolumeXMLWithBacking("clone.qcow2", volFormatQcow2, volUnitByte,
					21474836480, "/var/lib/libvirt/images/web-base.qcow2")
			},
			want: `<volume><name>clone.qcow2</name><capacity unit="B">21474836480</capacity>` +
				`<target><format type="qcow2"></format></target>` +
				`<backingStore><path>/var/lib/libvirt/images/web-base.qcow2</path>` +
				`<format type="qcow2"></format></backingStore></volume>`,
		},
		{
			name: "目录型存储池",
			got: func() (string, error) {
				return buildDirPoolXML("vmops", "/var/lib/libvirt/images")
			},
			want: `<pool type="dir"><name>vmops</name>` +
				`<target><path>/var/lib/libvirt/images</path></target></pool>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.got()
			if err != nil {
				t.Fatalf("生成 XML 失败: %v", err)
			}
			if got != tt.want {
				t.Errorf("XML 字符串不符\n期望: %s\n实际: %s", tt.want, got)
			}
		})
	}
}

// TestBuildVolumeXMLWithBackingStore 验证增量克隆（linked clone）的 <backingStore>。
//
// 风险点：AGENTS.md 第 11 条记录过一次严重返工——原实现用
// StorageVolCreateXMLFrom（等价 virsh vol-clone）做"增量克隆"，实测是全量拷贝，
// 子卷根本没有 backing file，功能名不副实。现在唯一让它成为增量卷的东西
// 就是这段 XML 里的 <backingStore><path>。因此必须验证：
// backingPath 为空时绝不出现该节点（否则普通建卷会变成挂在空路径上的增量卷），
// 非空时 path 精确等于父盘、format 声明为 qcow2。
func TestBuildVolumeXMLWithBackingStore(t *testing.T) {
	tests := []struct {
		name        string
		volName     string
		format      string
		unit        string
		capacity    int64
		backingPath string
		wantBacking bool
		wantBackFmt string
	}{
		{
			name:    "backingPath 为空：退化成普通卷，无 backingStore",
			volName: "plain.qcow2", format: volFormatQcow2, unit: volUnitGiB, capacity: 20,
			backingPath: "", wantBacking: false,
		},
		{
			name:    "backingPath 非空：生成增量子卷",
			volName: "web-clone.qcow2", format: volFormatQcow2, unit: volUnitByte, capacity: 21474836480,
			backingPath: "/var/lib/libvirt/images/web-base.qcow2",
			wantBacking: true, wantBackFmt: volFormatQcow2,
		},
		{
			name:    "子卷声明 raw、父盘仍按 qcow2 声明（现状）",
			volName: "odd.raw", format: "raw", unit: volUnitGiB, capacity: 20,
			backingPath: "/var/lib/libvirt/images/web-base.qcow2",
			// TODO(设计取舍): 父盘 format 在 buildVolumeXMLWithBacking 里被硬编码成 qcow2
			//  （storage.go:112），理由是"本项目所有池卷均为 qcow2"。
			//  但子卷 format 是外部可传的，raw 子卷 + qcow2 父盘这个组合本身不成立
			//  （raw 没有 backing file 的概念）。当前生成侧不拦，libvirt 会报错。
			//  若将来允许 raw 父盘，这里必须改成按父盘真实格式声明。
			wantBacking: true, wantBackFmt: volFormatQcow2,
		},
		{
			name:    "父盘路径含中文目录（合法 UTF-8）",
			volName: "cn-clone.qcow2", format: volFormatQcow2, unit: volUnitByte, capacity: 1024,
			backingPath: "/数据/镜像/基础盘.qcow2",
			wantBacking: true, wantBackFmt: volFormatQcow2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildVolumeXMLWithBacking(tt.volName, tt.format, tt.unit, tt.capacity, tt.backingPath)
			if err != nil {
				t.Fatalf("生成卷 XML 失败: %v", err)
			}
			v := unmarshalVolumeXML(t, got)

			if !tt.wantBacking {
				if len(v.BackingStores) != 0 {
					t.Fatalf("<backingStore> 节点数期望 0，实际 %d\nXML: %s", len(v.BackingStores), got)
				}
				return
			}

			if len(v.BackingStores) != 1 {
				t.Fatalf("<backingStore> 节点数期望 1，实际 %d\nXML: %s", len(v.BackingStores), got)
			}
			bs := v.BackingStores[0]
			if len(bs.Paths) != 1 {
				t.Fatalf("backingStore/path 节点数期望 1，实际 %d\nXML: %s", len(bs.Paths), got)
			}
			if bs.Paths[0] != tt.backingPath {
				t.Errorf("backingStore/path 期望 %q，实际 %q（写错父盘路径 = 增量链挂到错误的盘上）",
					tt.backingPath, bs.Paths[0])
			}
			if len(bs.Formats) != 1 {
				t.Fatalf("backingStore/format 节点数期望 1，实际 %d\nXML: %s", len(bs.Formats), got)
			}
			if bs.Formats[0].Type != tt.wantBackFmt {
				t.Errorf("backingStore/format 的 type 期望 %q，实际 %q", tt.wantBackFmt, bs.Formats[0].Type)
			}
			// backingStore 必须是 <volume> 的直接子节点，不能被塞进 <target> 里
			counts := xmlElementCounts(t, got)
			if counts["volume/backingStore"] != 1 {
				t.Errorf("volume/backingStore 路径计数期望 1，实际 %d\nXML: %s",
					counts["volume/backingStore"], got)
			}
			if counts["volume/target/backingStore"] != 0 {
				t.Errorf("backingStore 不应嵌在 <target> 内，实际 target 下有 %d 个\nXML: %s",
					counts["volume/target/backingStore"], got)
			}
		})
	}
}

// TestBuildVolumeXMLBlocksInjection 注入专项：卷名与格式塞满 XML 元字符和闭合标签载荷。
//
// 风险点：卷名与 format 都来自 HTTP 请求体（handler/storage.go 的 CreateVolume）。
// P1 之前这段 XML 是 fmt.Sprintf 拼的，卷名里写
// `x</name><target><path>/etc/passwd</path></target><name>y`
// 就能改掉卷的落盘路径——等于让请求方在宿主机上任意位置建文件。
// 现在改成 encoding/xml，但"改对了"需要证据，而且证据不能只是"字符串里有 &lt;"：
// 那只说明发生了替换，不说明结构没被撑开。
// 所以每个载荷都做三重验证：
//  1. 元字符已变成实体，且输出里 '<' '>' 的字面量个数恰好等于「元素数 × 2」
//     （assertXMLEscaped，多一个尖括号就意味着多一个标签）；
//  2. 反解后数节点——target、name、capacity 各自仍然只有 1 个，backingStore 仍是 0 个，
//     且没有多出 path/permissions 之类原本不该有的节点；
//  3. 字段值反解后与输入原文完全相等（转义是可逆的，没有吞字符也没有截断）。
func TestBuildVolumeXMLBlocksInjection(t *testing.T) {
	tests := []struct {
		name    string
		volName string
		format  string
	}{
		{
			name:    "卷名闭合 name 后追加 target/path（改写落盘路径）",
			volName: `x</name><target><path>/etc/cron.d/pwn</path></target><name>y`,
			format:  volFormatQcow2,
		},
		{
			name:    "卷名塞用户给的闭合载荷 /tmp/x</path></target><target><path>/",
			volName: `/tmp/x</path></target><target><path>/`,
			format:  volFormatQcow2,
		},
		{
			name:    "格式属性闭合双引号后追加节点",
			volName: "ok.qcow2",
			format:  `qcow2"/><permissions><owner>0</owner></permissions><format type="raw`,
		},
		{
			name:    "格式属性用单引号闭合",
			volName: "ok.qcow2",
			format:  `qcow2'/><encryption format='luks'/><format type='raw`,
		},
		{
			name:    "卷名塞整段 backingStore（想把普通卷变成挂在任意父盘上的增量卷）",
			volName: `a</name><backingStore><path>/etc/shadow</path><format type="raw"/></backingStore><name>b`,
			format:  volFormatQcow2,
		},
		{
			name:    "五个 XML 元字符全上",
			volName: `&<>"'`,
			format:  `&<>"'`,
		},
		{
			name:    "制表符/换行/回车（转成数字实体后必须能原样还原）",
			volName: "a\tb\nc\rd",
			format:  "e\tf",
		},
		{
			name:    "中文混元字符",
			volName: `系统盘<主>&<备>"'.qcow2`,
			format:  `qcow2<中文>`,
		},
		{
			name:    "注释与 CDATA 载荷",
			volName: `a<!--x--><![CDATA[<target><path>/tmp/p</path></target>]]>b`,
			format:  volFormatQcow2,
		},
		{
			name:    "实体引用载荷（不应被二次解析成 <）",
			volName: `a&lt;/name&gt;&amp;b`,
			format:  volFormatQcow2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildVolumeXML(tt.volName, tt.format, volUnitGiB, 20)
			if err != nil {
				t.Fatalf("注入载荷不应导致生成失败: %v", err)
			}

			// 1) 元字符必须被转义，且输出里的尖括号字面量只来自那 5 个合法元素
			assertXMLEscaped(t, got, 5, tt.volName, tt.format)

			// 2) 数节点：注入没有新增任何节点
			counts := xmlElementCounts(t, got)
			wantCounts := map[string]int{
				"volume":                     1,
				"volume/name":                1,
				"volume/capacity":            1,
				"volume/target":              1,
				"volume/target/format":       1,
				"volume/backingStore":        0,
				"volume/target/path":         0,
				"volume/target/permissions":  0,
				"volume/permissions":         0,
				"volume/encryption":          0,
				"volume/target/encryption":   0,
				"volume/backingStore/path":   0,
				"volume/backingStore/format": 0,
			}
			for path, want := range wantCounts {
				if counts[path] != want {
					t.Errorf("节点 %s 的个数期望 %d，实际 %d（说明注入改变了 XML 结构）\nXML: %s\n全量计数: %v",
						path, want, counts[path], got, counts)
				}
			}
			// 元素总数也钉住：volume + name + capacity + target + format = 5
			total := 0
			for _, c := range counts {
				total += c
			}
			if total != 5 {
				t.Errorf("元素总数期望 5（volume/name/capacity/target/format），实际 %d\n全量计数: %v", total, counts)
			}

			// 3) 反解后字段值与输入原文完全相等
			v := unmarshalVolumeXML(t, got)
			if len(v.Names) != 1 || v.Names[0] != tt.volName {
				t.Errorf("卷名反解后期望还原为原文 %q，实际 %v", tt.volName, v.Names)
			}
			if len(v.Targets) != 1 || len(v.Targets[0].Formats) != 1 {
				t.Fatalf("target/format 结构异常\nXML: %s", got)
			}
			if v.Targets[0].Formats[0].Type != tt.format {
				t.Errorf("format 反解后期望还原为原文 %q，实际 %q", tt.format, v.Targets[0].Formats[0].Type)
			}
			if len(v.Capacity) != 1 || v.Capacity[0].Value != "20" || v.Capacity[0].Unit != "G" {
				t.Errorf("capacity 被注入影响：期望 unit=G value=20，实际 %+v", v.Capacity)
			}
		})
	}
}

// TestBuildVolumeXMLControlCharBecomesReplacement 说明一个容易踩的边界：
// encoding/xml 遇到 XML 1.0 不允许的控制字符（如 0x00）不会报错，
// 而是替换成 U+FFFD（\uFFFD，"�"），因此这类输入无法原样还原。
//
// 风险点：注入专项里"反解后等于原文"是核心断言，但对含 NUL 的输入它不成立。
// 若不把这个例外写清楚，将来有人给注入用例加一条带 \x00 的载荷，
// 会误以为"字段值被吞了 = 转义有 bug"。真正需要注意的是相反的方向：
// 替换本身是安全的（不产生标签），但会静默改变卷名，
// 落盘文件名与用户输入不一致——所以入参校验层该拒绝控制字符。
func TestBuildVolumeXMLControlCharBecomesReplacement(t *testing.T) {
	tests := []struct {
		name    string
		volName string
		want    string
	}{
		{"NUL 字节被替换成 U+FFFD", "a\x00b.qcow2", "a\uFFFDb.qcow2"},
		{"0x01 控制字符同样被替换", "a\x01b", "a\uFFFDb"},
		{"制表符是合法字符，转义后可原样还原", "a\tb", "a\tb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildVolumeXML(tt.volName, volFormatQcow2, volUnitGiB, 1)
			if err != nil {
				t.Fatalf("含控制字符的输入现状下不报错，实际报错: %v", err)
			}
			v := unmarshalVolumeXML(t, got)
			if len(v.Names) != 1 {
				t.Fatalf("<name> 节点数期望 1，实际 %d\nXML: %s", len(v.Names), got)
			}
			if v.Names[0] != tt.want {
				t.Errorf("卷名反解结果期望 %q，实际 %q", tt.want, v.Names[0])
			}
			// 无论是否被替换，都不能产生新节点
			if counts := xmlElementCounts(t, got); counts["volume/name"] != 1 {
				t.Errorf("volume/name 节点数期望 1，实际 %d\nXML: %s", counts["volume/name"], got)
			}
		})
	}
}

// TestBuildDirPoolXMLStructure 锁定目录型存储池 XML：type 属性、池名、target/path。
//
// 风险点：<pool type='dir'> 的 type 决定 libvirt 用哪种后端驱动。
// 若 type 变成 logical/netfs，libvirt 会去操作 LVM 卷组或挂载远端 NFS，
// 而 <target><path> 的语义也随之改变——这是"定义存储池"这一步唯一不可逆的字段。
// path 则直接决定所有虚拟机磁盘落在宿主机哪个目录，写错等于把镜像写到系统盘。
func TestBuildDirPoolXMLStructure(t *testing.T) {
	tests := []struct {
		name     string
		poolName string
		path     string
	}{
		{"常规池", "vmops", "/var/lib/libvirt/images"},
		{"路径含连字符与下划线", "pool_test-1", "/data/kvm_pool-1"},
		{"中文池名与中文路径", "生产池", "/数据/镜像"},
		{"空池名与空路径（生成侧不校验，原样输出空元素）", "", ""},
		{"路径带空格", "sp", "/mnt/my pool/images"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildDirPoolXML(tt.poolName, tt.path)
			if err != nil {
				t.Fatalf("生成存储池 XML 失败: %v", err)
			}

			p := unmarshalPoolXML(t, got)
			if p.Type != poolTypeDir {
				t.Errorf("pool 的 type 属性期望 %q，实际 %q（type 决定 libvirt 用哪种存储后端）",
					poolTypeDir, p.Type)
			}
			if len(p.Names) != 1 {
				t.Fatalf("<name> 节点数期望 1，实际 %d\nXML: %s", len(p.Names), got)
			}
			if p.Names[0] != tt.poolName {
				t.Errorf("池名期望 %q，实际 %q", tt.poolName, p.Names[0])
			}
			if len(p.Targets) != 1 || len(p.Targets[0].Paths) != 1 {
				t.Fatalf("期望恰好 1 个 <target> 且其下 1 个 <path>，实际 target=%d\nXML: %s",
					len(p.Targets), got)
			}
			if p.Targets[0].Paths[0] != tt.path {
				t.Errorf("target/path 期望 %q，实际 %q（写错等于把所有虚拟机磁盘落到别的目录）",
					tt.path, p.Targets[0].Paths[0])
			}
			// 结构骨架固定：pool + name + target + path 共 4 个元素
			counts := xmlElementCounts(t, got)
			if counts["pool/target/path"] != 1 {
				t.Errorf("pool/target/path 计数期望 1，实际 %d\nXML: %s", counts["pool/target/path"], got)
			}
			total := 0
			for _, c := range counts {
				total += c
			}
			if total != 4 {
				t.Errorf("元素总数期望 4（pool/name/target/path），实际 %d\n全量计数: %v", total, counts)
			}
		})
	}
}

// TestBuildDirPoolXMLBlocksInjection 注入专项：池名与池路径塞元字符与闭合载荷。
//
// 风险点：池名与路径来自 handler/storage.go 的 CreatePool 请求体。
// P1 之前这里是字符串拼接，路径里写
// `/tmp/x</path></target><target><path>/`
// 能闭合 target 再开一个新的，libvirt 取最后一个 path 生效，
// 相当于请求方可以指定任意宿主机目录作为存储池根（含 / 与 /etc）。
// handler 侧现在有 validPoolPath/validVolName 白名单，但那是第一道防线，
// 生成侧的转义是第二道——两道都得有证据，何况将来可能新增不经 handler 的调用点。
func TestBuildDirPoolXMLBlocksInjection(t *testing.T) {
	tests := []struct {
		name     string
		poolName string
		path     string
	}{
		{
			name:     "路径塞闭合载荷 /tmp/x</path></target><target><path>/",
			poolName: "p1",
			path:     `/tmp/x</path></target><target><path>/`,
		},
		{
			name:     "池名闭合 name 后追加第二个 name",
			poolName: `p</name><name>evil`,
			path:     "/data/p",
		},
		{
			name:     "池名想改掉 type 属性（type 是属性，无法从子元素注入）",
			poolName: `p" type="logical`,
			path:     "/data/p",
		},
		{
			name:     "路径注入整段 permissions（想把池目录属主改成 root）",
			poolName: "p2",
			path:     `/data/p</path><permissions><owner>0</owner><group>0</group></permissions><path>/x`,
		},
		{
			name:     "池名与路径同时注入，且混中文",
			poolName: `<坏池名 & "引号">`,
			path:     `</path></target></pool><pool type="logical"><name>覆盖</name><target><path>/dev/vg0`,
		},
		{
			name:     "五个 XML 元字符全上",
			poolName: `&<>"'`,
			path:     `&<>"'`,
		},
		{
			name:     "制表符/换行/回车",
			poolName: "a\tb\nc\rd",
			path:     "/x\ty\nz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildDirPoolXML(tt.poolName, tt.path)
			if err != nil {
				t.Fatalf("注入载荷不应导致生成失败: %v", err)
			}

			// 1) 元字符必须被转义，且输出里的尖括号字面量只来自那 4 个合法元素
			assertXMLEscaped(t, got, 4, tt.poolName, tt.path)

			// 2) 数节点：结构与无注入时完全一致
			counts := xmlElementCounts(t, got)
			wantCounts := map[string]int{
				"pool":                    1,
				"pool/name":               1,
				"pool/target":             1,
				"pool/target/path":        1,
				"pool/permissions":        0,
				"pool/target/permissions": 0,
			}
			for path, want := range wantCounts {
				if counts[path] != want {
					t.Errorf("节点 %s 的个数期望 %d，实际 %d（说明注入改变了 XML 结构）\nXML: %s\n全量计数: %v",
						path, want, counts[path], got, counts)
				}
			}
			total := 0
			for _, c := range counts {
				total += c
			}
			if total != 4 {
				t.Errorf("元素总数期望 4（pool/name/target/path），实际 %d\n全量计数: %v", total, counts)
			}

			// 3) 反解后值还原为原文，且 type 仍是 dir（属性没被撬开）
			p := unmarshalPoolXML(t, got)
			if p.Type != poolTypeDir {
				t.Errorf("pool 的 type 属性期望仍为 %q，实际 %q（注入改写了存储后端类型）", poolTypeDir, p.Type)
			}
			if len(p.Names) != 1 || p.Names[0] != tt.poolName {
				t.Errorf("池名反解后期望还原为原文 %q，实际 %v", tt.poolName, p.Names)
			}
			if len(p.Targets) != 1 || len(p.Targets[0].Paths) != 1 {
				t.Fatalf("target/path 结构异常\nXML: %s", got)
			}
			if p.Targets[0].Paths[0] != tt.path {
				t.Errorf("池路径反解后期望还原为原文 %q，实际 %q", tt.path, p.Targets[0].Paths[0])
			}
		})
	}
}
