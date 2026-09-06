package handler

import (
	"regexp"
	"strings"
	"testing"
)

// TestValidPoolPath 覆盖目录型存储池路径校验（对应 virsh pool-define-as --target）。
//
// 风险点：该 path 决定池内所有卷的落盘位置，且会被序列化进 libvirt 池定义 XML。
// 零校验时调用方可以指定宿主机任意目录（把卷写进 /etc、/boot），也可以用 XML 元字符
// 闭合标签注入任意池定义。因此要求四条同时满足：绝对路径 + 字符白名单（无引号/尖括号/
// 空格/分号/美元符/反引号）+ 规范写法（filepath.Clean 后等于原值，借此拒 ..、//、结尾斜杠）
// + 不是根目录本身。virt 层改用 encoding/xml 序列化后，本函数是纵深防御的第一层。
func TestValidPoolPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		// —— 合法 ——
		{"标准 libvirt 镜像目录", "/var/lib/libvirt/images", true},
		{"单层目录", "/data", true},
		{"含下划线与连字符", "/data/vm_pool-01", true},
		{"含点（版本化目录名）", "/data/pool.v2", true},
		{"含数字", "/mnt/disk1/pool2", true},

		// —— 非绝对路径 ——
		{"相对路径", "var/lib/libvirt", false},
		{"当前目录相对写法", "./pool", false},
		{"上级目录相对写法", "../pool", false},
		{"空串", "", false},

		// —— 非规范写法（Clean 后与原值不等）——
		{"路径穿越 ..", "/var/lib/../../etc", false},
		{"末尾单个 ..", "/var/lib/..", false},
		{"结尾斜杠", "/var/lib/libvirt/images/", false},
		{"双斜杠", "//var/lib", false},
		{"中间双斜杠", "/var//lib", false},
		{"内嵌当前目录 .", "/var/./lib", false},
		{"根目录本身（拒绝，否则整盘都成池）", "/", false},
		{"含 .. 的合法目录名（保守拒绝）", "/data/..hidden", false},

		// —— 字符白名单：以下都是能污染 libvirt XML 或 shell 语义的字符 ——
		{"含空格", "/data/my pool", false},
		{"含分号", "/data/pool;rm -rf /", false},
		{"含单引号", "/data/pool'x", false},
		{"含双引号", `/data/pool"x`, false},
		{"含左尖括号", "/data/pool<x", false},
		{"含右尖括号", "/data/pool>x", false},
		{"含美元符（变量展开）", "/data/$HOME", false},
		{"含反引号（命令替换）", "/data/`id`", false},
		{"含管道符", "/data/pool|cat", false},
		{"含换行", "/data/pool\n/etc", false},
		{"含中文", "/data/存储池", false},
		{"含波浪号", "/data/~pool", false},
		{"含冒号", "/data/pool:2", false},
		{"含星号", "/data/pool*", false},

		// —— XML 闭合注入载荷（P1 修复的直接目标）——
		{"XML 闭合注入：伪造第二个 target", "/tmp/x</path></target><target><path>/", false},
		{"XML 属性注入", `/tmp/x" type="dir`, false},
		{"XML 注释注入", "/tmp/x<!--", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validPoolPath(tc.path); got != tc.want {
				t.Errorf("validPoolPath(%q) 判定错误：期望 %v，实际 %v", tc.path, tc.want, got)
			}
		})
	}
}

// TestValidVolFormat 覆盖卷格式白名单。
//
// 风险点：format 会写进卷 XML 的 <target><format type="..."/>。除了注入面，
// 放开非 qcow2/raw 的格式还会让「增量克隆」的 backingStore 语义失效（只有 qcow2 支持）。
// 空串是刻意放行的：由 virt 层 CreateVolumeCustom 兜默认 qcow2，保持老接口兼容。
func TestValidVolFormat(t *testing.T) {
	cases := []struct {
		name   string
		format string
		want   bool
	}{
		{"qcow2", "qcow2", true},
		{"raw", "raw", true},
		{"空串（由 virt 层兜默认 qcow2）", "", true},
		{"大写 QCOW2：白名单区分大小写，拒绝", "QCOW2", false},
		{"首字母大写 Qcow2", "Qcow2", false},
		{"未支持的 vmdk", "vmdk", false},
		{"未支持的 vdi", "vdi", false},
		{"带空格的 qcow2", " qcow2", false},
		{"结尾带空格", "qcow2 ", false},
		{"XML 属性闭合注入", `qcow2"/><x y="`, false},
		{"含分号", "qcow2;raw", false},
		{"纯空格", " ", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validVolFormat(tc.format); got != tc.want {
				t.Errorf("validVolFormat(%q) 判定错误：期望 %v，实际 %v", tc.format, tc.want, got)
			}
		})
	}
}

// TestValidVolName 覆盖卷/存储池名称校验。
//
// 风险点：名称同样进 libvirt XML 的 <name>。与 VM 名称的区别是这里允许点号
// （卷名带扩展名 img.qcow2 是常态），代价是 "." 与 ".." 这类路径特殊名也能通过正则 ——
// 它们会在 libvirt 侧查不到/建不成，且卷名从不参与宿主机路径拼接（见 virt.DeleteVolume
// 只做 StorageVolLookupByName），故不构成穿越。
func TestValidVolName(t *testing.T) {
	cases := []struct {
		name    string
		volName string
		want    bool
	}{
		{"常规卷名带扩展名", "web-01.qcow2", true},
		{"下划线", "web_01", true},
		{"纯数字", "20250906", true},
		{"点号开头（隐藏文件名）", ".hidden.qcow2", true},
		{"上级目录名 ..（正则放行，由 libvirt 侧兜住）", "..", true},
		{"空串", "", false},
		{"含斜杠（目录穿越形态）", "../../etc/passwd", false},
		{"含反斜杠", `web\01`, false},
		{"含空格", "web 01", false},
		{"含中文", "卷1", false},
		{"含分号", "web;id", false},
		{"含尖括号（XML 闭合）", "web</name><name>evil", false},
		{"含单引号", "web'01", false},
		{"含 %（URL 编码穿越）", "..%2fetc", false},
		{"含 NUL 字节", "web\x00.qcow2", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validVolName(tc.volName); got != tc.want {
				t.Errorf("validVolName(%q) 判定错误：期望 %v，实际 %v", tc.volName, tc.want, got)
			}
		})
	}
}

// TestValidateVMName 覆盖虚拟机/网络名称校验（vmNameRegex）。
//
// 风险点：名称会进 libvirt 域定义 XML、卷文件名（<name>.qcow2）、seed ISO 文件名。
// 白名单只放字母数字下划线连字符，是本项目所有「对象名」里最严的一档。
// 注意：网络创建（CreateNetwork）复用了本函数，改动会同时影响两处入口。
func TestValidateVMName(t *testing.T) {
	cases := []struct {
		name   string
		vmName string
		want   bool
	}{
		{"常规名称", "web-01", true},
		{"下划线", "web_01", true},
		{"纯字母", "web", true},
		{"纯数字", "01", true},
		{"大小写混合", "WebServer01", true},
		{"单字符", "a", true},
		{"仅连字符（正则放行，libvirt 侧可建）", "-", true},
		{"空串", "", false},
		{"含点号（与卷名规则不同，VM 名不允许点）", "web.01", false},
		{"含空格", "web 01", false},
		{"含中文", "网站服务器", false},
		{"含斜杠", "web/01", false},
		{"含分号", "web;reboot", false},
		{"含美元符", "web$1", false},
		{"含尖括号（XML 闭合注入）", "web</name><name>evil", false},
		{"含换行", "web\n01", false},
		{"含制表符", "web\t01", false},
		{"含 NUL 字节", "web\x0001", false},
		{"含 emoji", "web🚀", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateVMName(tc.vmName); got != tc.want {
				t.Errorf("validateVMName(%q) 判定错误：期望 %v，实际 %v", tc.vmName, tc.want, got)
			}
		})
	}
}

// TestValidIPv4 覆盖网络网关校验。
//
// 风险点：gateway 会写进 libvirt 网络 XML 的 <ip address='...'>。除注入面外，
// 实现额外要求「规范写法」（ip.String() == 输入），目的是排除 IPv6 与
// ::ffff:1.2.3.4 之类映射写法 —— 后者 To4() 非空，只靠 To4() 判定会被绕过。
func TestValidIPv4(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"常规网关", "192.168.100.1", true},
		{"10 段网关", "10.0.0.1", true},
		{"全零", "0.0.0.0", true},
		{"广播地址（格式合法，语义由 libvirt 兜）", "255.255.255.255", true},
		{"空串", "", false},
		{"IPv6 环回", "::1", false},
		{"IPv6 ULA", "fd00::1", false},
		{"IPv4-mapped 写法（To4 非空但非规范写法）", "::ffff:192.168.1.1", false},
		{"残缺三段", "1.2.3", false},
		{"五段", "1.2.3.4.5", false},
		{"越界 256", "256.1.1.1", false},
		{"负数段", "-1.1.1.1", false},
		{"前导零非规范写法", "192.168.001.1", false},
		{"带端口", "192.168.1.1:22", false},
		{"CIDR 写法", "192.168.1.0/24", false},
		{"带前导空格", " 192.168.1.1", false},
		{"带尾随空格", "192.168.1.1 ", false},
		{"XML 属性闭合注入", `1.2.3.4"><x/>`, false},
		{"主机名", "gateway.local", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validIPv4(tc.in); got != tc.want {
				t.Errorf("validIPv4(%q) 判定错误：期望 %v，实际 %v", tc.in, tc.want, got)
			}
		})
	}
}

// TestValidateSSHIP 覆盖宿主机地址校验（防 ping 的 argv 注入）。
//
// 风险点：该值最终作为 argv 传给 exec.Command("ping", "-c", "1", "-W", "2", host.SSHIP)。
// argv 形式不经 shell，所以分号/管道/反引号不会真的执行命令，但**以连字符开头的串会被
// ping 自己当成选项解析** —— "-f" 就是 flood ping（对目标发洪水包），"--help" 会让命令
// 立刻返回。因此必须在写库入口拦掉：先按 IP 解析，非 IP 时退回保守主机名白名单
// （首尾必须是字母或数字，中间只允许字母数字点连字符），长度上限 253（DNS 全长限制）。
func TestValidateSSHIP(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want bool
	}{
		// —— IP 形态 ——
		{"IPv4", "192.168.1.10", true},
		{"IPv4 环回（宿主机自身，允许）", "127.0.0.1", true},
		{"IPv6 完整写法", "2001:db8::1", true},
		{"IPv6 环回", "::1", true},

		// —— 主机名形态 ——
		{"短主机名", "kvm-host01", true},
		{"多级域名", "node-1.dc-2.internal", true},
		{"单字符主机名", "a", true},
		{"纯数字主机名（正则放行）", "12345", true},
		{"253 字符（DNS 长度上界）", strings.Repeat("a", 253), true},

		// —— 长度与空值 ——
		{"空串", "", false},
		{"254 字符超长", strings.Repeat("a", 254), false},
		{"1024 字符超长", strings.Repeat("a", 1024), false},

		// —— ping 选项注入（本函数的直接目标）——
		{"flood ping 短选项 -f", "-f", false},
		{"flood ping 长选项 --flood", "--flood", false},
		{"计数选项 -c1000000", "-c1000000", false},
		{"仅一个连字符", "-", false},
		{"连字符开头的主机名", "-host", false},

		// —— shell 元字符（argv 形式下不会执行，但一律拒绝，避免将来改成 shell 调用时踩雷）——
		{"分号拼接", "h;id", false},
		{"命令替换 $()", "$(id)", false},
		{"命令替换反引号", "`id`", false},
		{"管道符", "host|cat", false},
		{"与符号", "host&&id", false},
		{"重定向", "host>/tmp/x", false},
		{"含空格（会被拆成两个 argv）", "host 1", false},
		{"含换行", "host\nid", false},
		{"含 NUL 字节", "host\x00", false},

		// —— 主机名格式边界 ——
		{"点号开头", ".host", false},
		{"连字符结尾", "host-", false},
		{"点号结尾（根域写法，保守拒绝）", "host.", false},
		{"含下划线（主机名规范不允许）", "host_1", false},
		{"含中文", "宿主机", false},
		{"含冒号但非 IPv6", "host:22", false},
		{"含斜杠", "host/path", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateSSHIP(tc.addr); got != tc.want {
				t.Errorf("validateSSHIP(%q) 判定错误：期望 %v，实际 %v（len=%d）",
					tc.addr, tc.want, got, len(tc.addr))
			}
		})
	}
}

// TestSanitizeFileName 覆盖上传镜像的文件名清洗。
//
// 风险点：原始文件名来自 multipart 表单，完全可控，清洗后会与池目录拼成落盘路径。
// 实现是「先取 filepath.Base 再逐字符白名单」，双保险：Base 去掉目录部分，
// 白名单再抹掉剩余的特殊字符。任何一个分隔符漏进结果都意味着目录穿越。
func TestSanitizeFileName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"常规文件名", "ubuntu-22.04.qcow2", "ubuntu-22.04.qcow2"},
		{"绝对路径只留基名", "/etc/passwd", "passwd"},
		{"相对穿越只留基名", "../../etc/passwd", "passwd"},
		{"多层穿越 + 合法名", "../../../var/lib/libvirt/images/a.qcow2", "a.qcow2"},
		{"Windows 反斜杠路径（Linux 下 Base 不切分，冒号与反斜杠被抹掉）", `C:\win\a.qcow2`, "Cwina.qcow2"},
		{"空格被抹掉", "my image.qcow2", "myimage.qcow2"},
		{"含斜杠的注入载荷：Base 先切到最后一段", "a;rm -rf /.qcow2", ".qcow2"},
		{"分号与空格被抹掉（无斜杠）", "a;rm -rf x.qcow2", "arm-rfx.qcow2"},
		{"URL 编码穿越（% 被抹掉，不还原成斜杠）", "..%2fetc%2fpasswd", "..2fetc2fpasswd"},
		{"中文名只剩扩展名", "乌班图.qcow2", ".qcow2"},
		{"纯中文名清洗后为空（调用方另有兜底）", "乌班图", ""},
		{"空串（Base 返回 .）", "", "."},
		{"NUL 字节被抹掉", "a\x00.qcow2", "a.qcow2"},
		{"引号与尖括号被抹掉", `a<">.qcow2`, "a.qcow2"},
		{"美元符与反引号被抹掉", "a$(id)`x`.qcow2", "aidx.qcow2"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeFileName(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeFileName(%q) 错误：期望 %q，实际 %q", tc.in, tc.want, got)
			}
			// 不变式：输出永不含路径分隔符，否则 filepath.Join 会跳出池目录
			if strings.ContainsAny(got, `/\`) {
				t.Errorf("sanitizeFileName(%q) 输出仍含路径分隔符：%q", tc.in, got)
			}
		})
	}
}

// TestTaskUserFromContext 覆盖从 gin 上下文取操作者身份。
//
// 风险点：user_id/username 由 AuthMiddleware 写入，类型是 uint/string；
// 这里的两处类型断言都必须带 ok —— 上下文被别的中间件写成其他类型时只能降级为匿名，
// 不能 panic 掉整条请求链（P0 批次统一加固的断言保护之一）。
// 身份取不到只影响审计归属，取不到就写 nil/""，不阻断业务。
func TestTaskUserFromContext(t *testing.T) {
	cases := []struct {
		name         string
		set          map[string]interface{}
		wantID       *uint
		wantUsername string
	}{
		{"正常身份", map[string]interface{}{"user_id": uint(7), "username": "admin"}, uintPtr(7), "admin"},
		{"只有用户名", map[string]interface{}{"username": "viewer1"}, nil, "viewer1"},
		{"只有 ID", map[string]interface{}{"user_id": uint(1)}, uintPtr(1), ""},
		{"上下文为空（未过鉴权中间件）", nil, nil, ""},
		{"user_id 类型错误（int 而非 uint）：降级为 nil，不 panic",
			map[string]interface{}{"user_id": 7, "username": "admin"}, nil, "admin"},
		{"user_id 类型错误（string）：降级为 nil",
			map[string]interface{}{"user_id": "7"}, nil, ""},
		{"username 类型错误（int）：降级为空串",
			map[string]interface{}{"user_id": uint(3), "username": 3}, uintPtr(3), ""},
		{"值为 nil：降级", map[string]interface{}{"user_id": nil, "username": nil}, nil, ""},
		{"中文用户名原样透出", map[string]interface{}{"username": "张三"}, nil, "张三"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestContext("/api/vms", nil)
			for k, v := range tc.set {
				c.Set(k, v)
			}

			gotID, gotUsername := taskUserFromContext(c)

			if gotUsername != tc.wantUsername {
				t.Errorf("username 错误：期望 %q，实际 %q", tc.wantUsername, gotUsername)
			}
			switch {
			case tc.wantID == nil && gotID != nil:
				t.Errorf("user_id 错误：期望 nil，实际 %d", *gotID)
			case tc.wantID != nil && gotID == nil:
				t.Errorf("user_id 错误：期望 %d，实际 nil", *tc.wantID)
			case tc.wantID != nil && *gotID != *tc.wantID:
				t.Errorf("user_id 错误：期望 %d，实际 %d", *tc.wantID, *gotID)
			}
		})
	}

	t.Run("返回的是值拷贝：两次调用得到互不影响的指针", func(t *testing.T) {
		c, _ := newTestContext("/api/vms", nil)
		c.Set("user_id", uint(9))

		first, _ := taskUserFromContext(c)
		second, _ := taskUserFromContext(c)
		if first == nil || second == nil {
			t.Fatal("两次调用都应取到 user_id")
		}
		if first == second {
			t.Error("两次调用返回了同一个指针，调用方改一处会影响另一处")
		}
		*first = 100
		if *second != 9 {
			t.Errorf("指针互相污染：改第一个后第二个变成 %d，期望仍为 9", *second)
		}
	})
}

// uintPtr 便于在表里写期望的 *uint。
func uintPtr(v uint) *uint { return &v }

// macFormatHandler / uuidV4FormatHandler 与 service/virt、service/tasks 里的同名校验一致。
var (
	macFormatHandler    = regexp.MustCompile(`^52:54:00:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}$`)
	uuidV4FormatHandler = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// TestHandlerRandomMACAndUUID 覆盖 handler 包内的 MAC/UUID 生成器。
//
// 风险点：MAC 重复会让同网段两台机 ARP 冲突、网络双双不可用（P2 批次修过一次同类缺陷）；
// UUID 重复会撞 vms.uuid 唯一索引导致创建失败。注意这两个函数在
// handler/vm.go、service/tasks/vm_tasks.go、service/virt/clone.go 里各有一份**独立实现**，
// 三处都要各自有测试，改一处不会自动惠及另外两处。
func TestHandlerRandomMACAndUUID(t *testing.T) {
	t.Run("MAC 前缀固定 52:54:00 且 300 次不重复", func(t *testing.T) {
		seen := make(map[string]bool, 300)
		for i := 0; i < 300; i++ {
			mac, err := randomMAC()
			if err != nil {
				t.Fatalf("第 %d 次生成 MAC 失败: %v", i, err)
			}
			if !macFormatHandler.MatchString(mac) {
				t.Fatalf("MAC 格式不符（期望 52:54:00:xx:xx:xx）：实际 %q", mac)
			}
			if seen[mac] {
				t.Fatalf("300 次生成出现重复 MAC：%s（同网段会 ARP 冲突）", mac)
			}
			seen[mac] = true
		}
	})

	t.Run("UUID 符合 RFC 4122 v4 且 300 次不重复", func(t *testing.T) {
		seen := make(map[string]bool, 300)
		for i := 0; i < 300; i++ {
			uuid, err := randomUUID()
			if err != nil {
				t.Fatalf("第 %d 次生成 UUID 失败: %v", i, err)
			}
			if !uuidV4FormatHandler.MatchString(uuid) {
				t.Fatalf("UUID 不符合 v4 格式（第三段须以 4 开头，第四段首字符须为 8/9/a/b）：实际 %q", uuid)
			}
			if seen[uuid] {
				t.Fatalf("300 次生成出现重复 UUID：%s（会撞 vms.uuid 唯一索引）", uuid)
			}
			seen[uuid] = true
		}
	})
}

// TestFormatBytes 覆盖宿主机内存展示的单位换算。
// 风险点：进制写错（1000 vs 1024）或边界写错会让概览页显示离谱数值；TB 是最后一档，
// 超过 TB 必须停在 TB 而不是继续除下去得到空单位。
func TestFormatBytes(t *testing.T) {
	cases := []struct {
		name string
		in   uint64
		want string
	}{
		{"零字节", 0, "0 B"},
		{"1023 字节仍按 B 显示", 1023, "1023 B"},
		{"1024 字节整好 1KB", 1024, "1.0 KB"},
		{"1.5KB 保留一位小数", 1536, "1.5 KB"},
		{"1MB", 1024 * 1024, "1.0 MB"},
		{"1GB", 1024 * 1024 * 1024, "1.0 GB"},
		{"16GB（常见宿主机内存）", 16 * 1024 * 1024 * 1024, "16.0 GB"},
		{"1TB", 1024 * 1024 * 1024 * 1024, "1.0 TB"},
		{"2048TB 超上限也停在 TB", 2048 * 1024 * 1024 * 1024 * 1024, "2048.0 TB"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatBytes(tc.in); got != tc.want {
				t.Errorf("formatBytes(%d) 错误：期望 %q，实际 %q", tc.in, tc.want, got)
			}
		})
	}
}

// TestFormatUptimeCN 覆盖运行时长的中文格式化。
// 风险点：/proc/uptime 读失败时返回 0，此时必须显示占位符而不是「0 分钟」（会被误读成刚重启）。
func TestFormatUptimeCN(t *testing.T) {
	cases := []struct {
		name string
		sec  int64
		want string
	}{
		{"读取失败（0 秒）显示占位符", 0, "—"},
		{"负数同样显示占位符", -1, "—"},
		{"不足一分钟显示 0 分钟", 30, "0 分钟"},
		{"整分钟", 120, "2 分钟"},
		{"跨小时", 3661, "1 小时 1 分钟"},
		{"整小时", 7200, "2 小时 0 分钟"},
		{"跨天", 90061, "1 天 1 小时 1 分钟"},
		{"多天", 8 * 86400, "8 天 0 小时 0 分钟"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatUptimeCN(tc.sec); got != tc.want {
				t.Errorf("formatUptimeCN(%d) 错误：期望 %q，实际 %q", tc.sec, tc.want, got)
			}
		})
	}
}

// TestParseMeminfoKB 覆盖 /proc/meminfo 单行解析。
// 风险点：不同内核/容器环境下该文件的空格数、字段数都可能不同，解析失败必须返回 0
// 让上层显示为空，而不是 panic 或给出错误数值。
func TestParseMeminfoKB(t *testing.T) {
	cases := []struct {
		name string
		line string
		want uint64
	}{
		{"标准行", "MemTotal:       16316160 kB", 16316160},
		{"单空格分隔", "MemAvailable: 8192 kB", 8192},
		{"制表符分隔", "MemFree:\t4096 kB", 4096},
		{"无单位后缀", "MemTotal: 1024", 1024},
		{"字段不足", "MemTotal:", 0},
		{"空行", "", 0},
		{"数值非法", "MemTotal: N/A kB", 0},
		{"数值为负", "MemTotal: -1 kB", 0},
		{"数值带小数", "MemTotal: 10.5 kB", 0},
		{"零值", "MemTotal: 0 kB", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseMeminfoKB(tc.line); got != tc.want {
				t.Errorf("parseMeminfoKB(%q) 错误：期望 %d，实际 %d", tc.line, tc.want, got)
			}
		})
	}
}
