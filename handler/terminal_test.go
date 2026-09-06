package handler

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/jiuzhao/vmops/model"
)

// assertChineseRejection 校验拒绝原因是「中文 + 不含内部细节」。
//
// validateSSHTarget 的返回值会被直接 WriteJSON 回浏览器终端（terminal.go 里
// `conn.WriteJSON(gin.H{"type":"error","msg": err.Error()})`），所以它同时是
// 用户可见文案与对外输出面：必须是中文，且不能带 Go 错误链、堆栈、宿主机路径等痕迹。
func assertChineseRejection(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("期望返回拒绝原因，实际为 nil")
	}
	msg := err.Error()
	if !containsCJK(msg) {
		t.Errorf("拒绝原因不是中文，会直接显示在前端终端上：%q", msg)
	}
	for _, leak := range []string{"panic", "goroutine", "libvirt", "gorm", "sql", "/home/", "/var/run", "0x"} {
		if strings.Contains(strings.ToLower(msg), leak) {
			t.Errorf("拒绝原因泄漏内部细节 %q：%q", leak, msg)
		}
	}
}

// TestValidateSSHTargetWithRecordedIP 覆盖「平台已记录 VM 地址」这条最强约束。
//
// 风险点：Web 终端的 host/port/user/password 全部来自浏览器首帧。若不校验，
// 任何登录用户都能驱动服务器向任意地址发起 SSH —— 平台立刻变成跳板机、内网端口扫描器
// 与口令爆破器。本分支要求目标与库里记录的地址**精确一致**（记录值做 TrimSpace），
// 不一致时错误文案要点出正确地址，方便正常用户自查。
//
// ⚠️ 现状备注（见回报「疑似问题」）：全仓库没有任何代码写 vms.ip，
// 因此这条分支目前只有手工改库才会触发，线上实际都走下面的私有网段分支。
// 本用例仍完整覆盖，作为将来接入 guest agent / DHCP lease 回填后的回归锚点。
func TestValidateSSHTargetWithRecordedIP(t *testing.T) {
	cases := []struct {
		name     string
		recorded string // vm.IP
		host     string // 浏览器传来的目标
		wantErr  bool
		errHas   string // 期望错误文案包含的内容
	}{
		{"目标与记录地址一致：放行", "192.168.122.50", "192.168.122.50", false, ""},
		{"目标与记录地址不一致：拒绝且提示正确地址", "192.168.122.50", "192.168.122.51", true, "192.168.122.50"},
		{"记录值带首尾空格：TrimSpace 后仍能匹配", "  192.168.122.50  ", "192.168.122.50", false, ""},
		{"记录值只有空白字符：视为未记录，改走私有网段判定（私有地址放行）", "   ", "10.0.0.5", false, ""},
		{"记录值只有空白字符：改走私有网段判定（公网地址拒绝）", "   ", "8.8.8.8", true, "私有网段"},
		{"已记录 IP 时目标传主机名：拒绝", "192.168.122.50", "vm-01", true, "192.168.122.50"},
		{"已记录 IP 时目标传环回地址：拒绝", "192.168.122.50", "127.0.0.1", true, "192.168.122.50"},
		{"目标带前导空格：host 侧不做 TrimSpace，判定不相等而拒绝", "192.168.122.50", " 192.168.122.50", true, "192.168.122.50"},
		// 该分支不再做网段判定：库里记录什么就只允许连什么（信任平台自身记录的地址）
		{"记录值为公网地址时目标一致：现状放行（该分支跳过网段判定）", "203.0.113.9", "203.0.113.9", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := &model.VM{Name: "web-01", IP: tc.recorded}
			err := validateSSHTarget(vm, tc.host, 22)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("期望放行 host=%q（记录值 %q），实际被拒：%v", tc.host, tc.recorded, err)
				}
				return
			}
			assertChineseRejection(t, err)
			if tc.errHas != "" && !strings.Contains(err.Error(), tc.errHas) {
				t.Errorf("拒绝原因缺少关键信息：期望包含 %q，实际 %q", tc.errHas, err.Error())
			}
		})
	}
}

// TestValidateSSHTargetWithoutRecordedIP 覆盖「未记录 VM 地址」时的白名单判定（线上常态分支）。
//
// 风险点逐条对应实现里的四道判定：
//   - 必须是 IP 字面量：接受主机名意味着解析结果由 DNS 决定，可指向公网，也可做 DNS rebinding；
//   - 排除环回：127.0.0.0/8 指向宿主机自身，放过等于允许 SSH 进 KVM 宿主机；
//   - 排除链路本地：169.254.169.254 是云厂商元数据端点，能取到实例凭证；
//   - 排除组播/未指定，且必须落在 RFC1918 私有网段内，否则平台可被用来打公网。
func TestValidateSSHTargetWithoutRecordedIP(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		wantErr bool
		errHas  string
	}{
		// —— 私有网段：放行 ——
		{"私有网段 10/8", "10.0.0.5", false, ""},
		{"私有网段 10/8 边界（10.255.255.254）", "10.255.255.254", false, ""},
		{"私有网段 172.16/12 起始", "172.16.0.1", false, ""},
		{"私有网段 172.16/12 末端（172.31.255.254）", "172.31.255.254", false, ""},
		{"私有网段 192.168/16（libvirt default 网段）", "192.168.122.50", false, ""},

		// —— 私有网段边界外：拒绝 ——
		{"172.32.0.1 超出 172.16/12 上界", "172.32.0.1", true, "私有网段"},
		{"172.15.0.1 低于 172.16/12 下界", "172.15.0.1", true, "私有网段"},
		{"11.0.0.1 紧邻 10/8 之外", "11.0.0.1", true, "私有网段"},
		{"100.64.0.1 运营商级 NAT（非 RFC1918）", "100.64.0.1", true, "私有网段"},

		// —— 公网：拒绝（防止平台被当成扫描器/爆破器）——
		{"公网 DNS 1.1.1.1", "1.1.1.1", true, "私有网段"},
		{"公网 DNS 8.8.8.8", "8.8.8.8", true, "私有网段"},
		{"公网 DNS 223.5.5.5", "223.5.5.5", true, "私有网段"},
		{"文档保留地址 203.0.113.9", "203.0.113.9", true, "私有网段"},

		// —— 特殊用途地址：拒绝 ——
		{"环回 127.0.0.1（放行即可 SSH 进宿主机自身）", "127.0.0.1", true, "环回"},
		{"环回段内任意地址 127.1.2.3", "127.1.2.3", true, "环回"},
		{"链路本地 169.254.169.254（云元数据端点）", "169.254.169.254", true, "链路本地"},
		{"链路本地 169.254.1.1", "169.254.1.1", true, "链路本地"},
		{"未指定地址 0.0.0.0", "0.0.0.0", true, "环回"},
		{"组播 224.0.0.1", "224.0.0.1", true, "组播"},
		{"组播 239.255.255.250（SSDP）", "239.255.255.250", true, "组播"},
		{"广播地址 255.255.255.255", "255.255.255.255", true, "私有网段"},

		// —— 主机名：一律拒绝（防 DNS 解析到公网与 DNS rebinding）——
		{"域名 example.com", "example.com", true, "IP 地址"},
		{"localhost（解析后即环回）", "localhost", true, "IP 地址"},
		{"短主机名 vm-01", "vm-01", true, "IP 地址"},
		{"内网 FQDN db.internal", "db.internal", true, "IP 地址"},
		{"空 host（Connect 里已提前拦，此处兜底）", "", true, "IP 地址"},
		{"IP 带端口写法 10.0.0.5:22", "10.0.0.5:22", true, "IP 地址"},
		{"IP 带 CIDR 写法 10.0.0.0/8", "10.0.0.0/8", true, "IP 地址"},
		{"非规范写法 010.0.0.5（Go 拒绝前导零）", "010.0.0.5", true, "IP 地址"},
		{"残缺 IP 1.2.3", "1.2.3", true, "IP 地址"},
		{"越界 IP 256.1.1.1", "256.1.1.1", true, "IP 地址"},

		// —— IPv6：按实现的实际语义断言 ——
		{"IPv6 环回 ::1：拒绝", "::1", true, "环回"},
		{"IPv6 未指定 :: ：拒绝", "::", true, "环回"},
		{"IPv6 链路本地 fe80::1：拒绝", "fe80::1", true, "链路本地"},
		{"IPv6 公网 2001:db8::1：拒绝", "2001:db8::1", true, "私有网段"},
		{"IPv6 组播 ff02::1：拒绝", "ff02::1", true, "组播"},
		// net.IP.IsPrivate 对 fc00::/7（ULA）返回 true，因此 IPv6 私有地址目前被放行；
		// 而错误文案只写了三个 IPv4 网段，属于文案与实现不完全对齐（见回报「疑似问题」）。
		{"IPv6 ULA fd00::1：现状放行（IsPrivate 对 fc00::/7 为 true）", "fd00::1", false, ""},
		{"IPv6 ULA fc00::1：现状同样放行", "fc00::1", false, ""},
		// IPv4-mapped 写法经 ParseIP 后与 192.168.1.1 等价，故按私有地址放行
		{"IPv4-mapped ::ffff:192.168.1.1：现状放行（等价私有 IPv4）", "::ffff:192.168.1.1", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := &model.VM{Name: "web-01"} // IP 为空 → 走白名单分支
			err := validateSSHTarget(vm, tc.host, 22)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("期望放行 host=%q，实际被拒：%v", tc.host, err)
				}
				return
			}
			assertChineseRejection(t, err)
			if tc.errHas != "" && !strings.Contains(err.Error(), tc.errHas) {
				t.Errorf("拒绝原因分类不符：host=%q 期望文案包含 %q，实际 %q", tc.host, tc.errHas, err.Error())
			}
		})
	}
}

// TestValidateSSHTargetPortBoundary 覆盖端口范围校验。
//
// 风险点：端口同样来自浏览器。负数/0/超 65535 会被 net.JoinHostPort 拼成非法目标，
// 而 1-65535 全开放意味着可以拿平台探测 VM 上的任意服务端口（口令爆破面）。
// 校验顺序上端口在最前：即使 host 合法，端口越界也必须先被拒。
func TestValidateSSHTargetPortBoundary(t *testing.T) {
	cases := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"端口 0（未填时 Connect 会补 22，这里是兜底）", 0, true},
		{"端口 -1", -1, true},
		{"端口 -22", -22, true},
		{"端口 65536 越上界", 65536, true},
		{"端口 99999", 99999, true},
		{"端口 1 下界放行", 1, false},
		{"端口 22 常规 SSH", 22, false},
		{"端口 2222 常见转发端口", 2222, false},
		{"端口 65535 上界放行", 65535, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := &model.VM{Name: "web-01"}
			err := validateSSHTarget(vm, "192.168.122.50", tc.port)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("期望放行端口 %d，实际被拒：%v", tc.port, err)
				}
				return
			}
			assertChineseRejection(t, err)
			if !strings.Contains(err.Error(), "端口") {
				t.Errorf("端口越界的拒绝文案未点明端口：port=%d 实际 %q", tc.port, err.Error())
			}
		})
	}

	// 校验顺序：端口非法时不应该先报 host 的问题（否则用户按提示改 host 也修不好）
	t.Run("端口非法优先于 host 非法上报", func(t *testing.T) {
		vm := &model.VM{Name: "web-01"}
		err := validateSSHTarget(vm, "8.8.8.8", 0)
		if err == nil {
			t.Fatal("端口与 host 双非法时期望被拒，实际放行")
		}
		if !strings.Contains(err.Error(), "端口") {
			t.Errorf("期望先报端口问题，实际 %q", err.Error())
		}
	})
}

// TestItoa 覆盖 terminal.go 里自实现的整型转字符串（拼 SSH 目标端口用）。
//
// 风险点：手写数字转换极易在边界上出错，而它的输出直接进 net.JoinHostPort。
// 这里用 strconv.Itoa 做差分对照（oracle），任何不一致都能立刻定位。
func TestItoa(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"零", 0, "0"},
		{"一位数", 7, "7"},
		{"常规端口 22", 22, "22"},
		{"端口上界 65535", 65535, "65535"},
		{"负数", -1, "-1"},
		{"多位负数", -65535, "-65535"},
		{"int64 上界", math.MaxInt64, "9223372036854775807"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := itoa(tc.in); got != tc.want {
				t.Errorf("itoa(%d) 错误：期望 %q，实际 %q", tc.in, tc.want, got)
			}
		})
	}

	t.Run("与 strconv.Itoa 差分对照（-2000..2000 及若干大值）", func(t *testing.T) {
		for n := -2000; n <= 2000; n++ {
			if got, want := itoa(n), strconv.Itoa(n); got != want {
				t.Fatalf("itoa(%d) 与 strconv.Itoa 不一致：期望 %q，实际 %q", n, want, got)
			}
		}
		for _, n := range []int{65535, 65536, 1 << 20, math.MaxInt32, math.MaxInt64, -math.MaxInt64} {
			if got, want := itoa(n), strconv.Itoa(n); got != want {
				t.Fatalf("itoa(%d) 与 strconv.Itoa 不一致：期望 %q，实际 %q", n, want, got)
			}
		}
	})

	// TODO(已在回报中列出): math.MinInt64 取反溢出，实现返回单个 "-"（strconv.Itoa 给
	// "-9223372036854775808"）。此处断言现状而非期望值：itoa 的唯一调用点是已经过
	// validateSSHTarget 校验（1-65535）的端口，该输入不可达，故不改生产代码。
	t.Run("math.MinInt64：现状返回单个减号（取反溢出，实际不可达）", func(t *testing.T) {
		got := itoa(math.MinInt64)
		if got != "-" {
			t.Errorf("现状变更：itoa(math.MinInt64) 原本返回 %q，实际 %q（若已修复请更新本用例）", "-", got)
		}
		if got == strconv.Itoa(math.MinInt64) {
			t.Errorf("实现已与 strconv 对齐，本用例应改为正向断言")
		}
	})
}
