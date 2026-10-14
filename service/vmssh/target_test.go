package vmssh

import "testing"

// TestValidateTarget 逐条锁死拨号目标白名单（ValidateTarget）。
//
// 风险点：这是「平台会不会变成免费跳板机」的唯一收口——SSH 拨号参数源自浏览器请求体，
// 历史上一旦漏校验，任何登录用户都能驱动服务器向任意公网/内网地址发起 SSH（内网横扫、
// 端口扫描、口令爆破，源 IP 全算在服务器上）。约束由强到弱三条：
//  1. 平台已记录 VM 的 IP 时，目标必须与记录精确一致（TrimSpace 后比对）；
//  2. 未记录 IP 时只接受私有网段的 IP 字面量（IPv4 RFC1918 / IPv6 ULA fd00::/8），
//     并排除环回、未指定、链路本地、组播；不接受主机名（防 DNS 解析/rebinding 绕过）；
//  3. 端口必须落在 1-65535。
//
// 每条 case 都对应一个真实的绕过路径，缺一条断言就可能放进一类外连目标。
func TestValidateTarget(t *testing.T) {
	tests := []struct {
		name       string
		recordedIP string
		host       string
		port       int
		wantErr    bool
	}{
		// —— 端口上界/下界（在目标判定之前先判，故 recordedIP 为空也不影响）——
		{"端口 0 非法", "", "10.0.0.5", 0, true},
		{"端口 65536 越界", "", "10.0.0.5", 65536, true},
		{"端口负数", "", "10.0.0.5", -5, true},
		{"端口 1 合法边界", "", "10.0.0.5", 1, false},
		{"端口 65535 合法边界", "", "10.0.0.5", 65535, false},

		// —— 平台已记录 IP：只允许连它自己 ——
		{"记录 IP 与目标一致", "192.168.1.10", "192.168.1.10", 22, false},
		{"记录 IP 与目标不一致（哪怕同网段）", "192.168.1.10", "192.168.1.11", 22, true},
		{"记录 IP 前后带空白也应精确匹配", " 192.168.1.10 ", "192.168.1.10", 22, false},
		{"记录 IP 为空串视同未记录", "", "192.168.1.10", 22, false},

		// —— 未记录 IP：仅私有网段 IP 字面量 ——
		{"IPv4 10.x 私有段", "", "10.0.0.0", 22, false},
		{"IPv4 172.16.x 私有段", "", "172.16.0.1", 22, false},
		{"IPv4 192.168.x 私有段", "", "192.168.0.5", 22, false},
		{"IPv6 ULA fd00::/8", "", "fd00::1", 22, false},
		{"公网 IPv4 拒绝", "", "8.8.8.8", 22, true},
		{"公网 IPv6（非 ULA）拒绝", "", "2001:db8::1", 22, true},
		{"环回 IPv4 拒绝（否则可 SSH 进宿主机）", "", "127.0.0.1", 22, true},
		{"环回 IPv6 拒绝", "", "::1", 22, true},
		{"未指定地址 0.0.0.0 拒绝", "", "0.0.0.0", 22, true},
		{"链路本地 169.254.x 拒绝", "", "169.254.1.1", 22, true},
		{"组播 224.0.0.1 拒绝", "", "224.0.0.1", 22, true},
		{"主机名拒绝（防 DNS 绕过）", "", "example.com", 22, true},
		{"主机名（解析到私网的写法）也拒绝", "", "localhost", 22, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTarget(tt.recordedIP, tt.host, tt.port)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateTarget(%q, %q, %d) 应被拒，实际放行", tt.recordedIP, tt.host, tt.port)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateTarget(%q, %q, %d) 应放行，实际报错: %v", tt.recordedIP, tt.host, tt.port, err)
			}
		})
	}
}

// TestNewOptionsPortDefaultAndBounds 补齐 NewOptions 的端口默认与校验边界。
//
// 风险点：NewOptions 是「绕过白名单直接外连」在编译期就被堵死的关键——Options 字段
// 全不可导出，外部无法自拼字面量。port<=0 时先补默认 22 再校验（ValidateTarget 要求
// 端口落在 1-65535，若顺序写反，port=0 会被「端口不合法」误拒而非补 22）。
// 这里确认缺省补 22、越界端口仍被拒、非法目标经此入口一律拿不到 Options。
func TestNewOptionsPortDefaultAndBounds(t *testing.T) {
	// 端口 0 / 负数：补默认 22 后应通过私有网段校验。
	for _, port := range []int{0, -1} {
		opts, err := NewOptions("", "10.0.0.9", port, "root", "pwd")
		if err != nil {
			t.Fatalf("端口 %d 应补默认 22 后放行，实际报错: %v", port, err)
		}
		if opts.Port() != 22 {
			t.Fatalf("端口 %d 缺省应为 22，得到 %d", port, opts.Port())
		}
	}
	// 越界端口：显式给定，不走默认，必须被拒。
	if _, err := NewOptions("", "10.0.0.9", 70000, "root", "pwd"); err == nil {
		t.Fatal("越界端口 70000 应被拒绝")
	}
	// 非法目标：构造失败必须返回零值 Options（validated=false），Dial 才能拦下。
	opts, err := NewOptions("", "8.8.8.8", 22, "root", "pwd")
	if err == nil {
		t.Fatal("公网目标经 NewOptions 应报错")
	}
	if opts.validated {
		t.Fatal("构造失败不得返回已校验的 Options（否则可绕过白名单拨号）")
	}
}
