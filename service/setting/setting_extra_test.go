package setting

import (
	"strings"
	"testing"
	"time"
)

// TestValidateExtraKeys 补齐既有 setting_test.go 未覆盖的键分支。
//
// 风险点：Validate 是「配置写入落库前」的唯一闸门，且这些键直接落在安全面上——
// security_entrance 是登录的第二道口令（写错校验会放行弱/可绕过的暗号），
// password_min_length 是口令强度策略，ai_* 三键与 alert_notify_url 是出站凭据/地址。
// 既有测试只覆盖了池名/两个 TTL/公告/告警地址，本用例把剩余键的
// 「空值、纯空白、越界长度、非法字符」逐条钉死，防止将来有人放宽正则或漏掉分支。
func TestValidateExtraKeys(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		// AI 三键：长度 1-300，空/纯空白一律拒绝（ai_api_key 尤其不能接受空）
		{"AI 地址合法", KeyAIBaseURL, "https://api.deepseek.com/v1", false},
		{"AI 地址为空拒绝", KeyAIBaseURL, "", true},
		{"AI 地址纯空白拒绝", KeyAIBaseURL, "   ", true},
		{"AI 地址恰在 300 上界", KeyAIBaseURL, strings.Repeat("a", 300), false},
		{"AI 地址超 300 拒绝", KeyAIBaseURL, strings.Repeat("a", 301), true},
		{"AI 模型名为空拒绝", KeyAIModel, "", true},
		{"AI 模型名合法", KeyAIModel, "deepseek-chat", false},
		{"AI 模型名超 300 拒绝", KeyAIModel, strings.Repeat("m", 301), true},
		{"AI API Key 为空拒绝", KeyAIAPIKey, "", true},
		{"AI API Key 纯空白拒绝", KeyAIAPIKey, "  ", true},
		{"AI API Key 合法", KeyAIAPIKey, "sk-abcdef123456", false},
		{"AI API Key 超 300 拒绝", KeyAIAPIKey, strings.Repeat("k", 301), true},

		// 跳板命令黑名单：空值合法（回落到内置默认），有值限 500
		{"黑名单空值合法", KeyJumpdCmdBlacklist, "", false},
		{"黑名单正常值合法", KeyJumpdCmdBlacklist, "rm,-rf,/etc/shadow", false},
		{"黑名单恰在 500 上界", KeyJumpdCmdBlacklist, strings.Repeat("x", 500), false},
		{"黑名单超 500 拒绝", KeyJumpdCmdBlacklist, strings.Repeat("x", 501), true},

		// 安全入口：空=关闭；非空仅接受 URL/头安全字符（字母数字 _ -），且不收纯空白
		{"安全入口空值关闭", KeySecurityEntrance, "", false},
		{"安全入口纯空白拒绝", KeySecurityEntrance, "   ", true},
		{"安全入口合法字符集", KeySecurityEntrance, "abC_123-XYZ", false},
		{"安全入口含空格拒绝", KeySecurityEntrance, "ab cd", true},
		{"安全入口含中文拒绝", KeySecurityEntrance, "口令", true},
		{"安全入口含斜杠拒绝", KeySecurityEntrance, "a/b", true},
		{"安全入口恰在 64 上界", KeySecurityEntrance, strings.Repeat("a", 64), false},
		{"安全入口超 64 拒绝", KeySecurityEntrance, strings.Repeat("a", 65), true},

		// 密码最小长度：0=关闭策略，上界 64
		{"密码长度 0 关闭合法", KeyPasswordMinLength, "0", false},
		{"密码长度下界之上合法", KeyPasswordMinLength, "8", false},
		{"密码长度恰在上界", KeyPasswordMinLength, "64", false},
		{"密码长度超上界拒绝", KeyPasswordMinLength, "65", true},
		{"密码长度负数拒绝", KeyPasswordMinLength, "-1", true},
		{"密码长度非数字拒绝", KeyPasswordMinLength, "abc", true},

		// token 有效期下界（既有测试只测了上界与 1/60）
		{"token 有效期 0 越下界", KeyVNCTokenTTLMin, "0", true},

		// 池名：单字符与 64 上界（既有测试覆盖了空、含点、含路径、超长、非法字符）
		{"池名单字符合法", KeyDefaultStoragePool, "a", false},
		{"池名恰在 64 上界", KeyDefaultStoragePool, strings.Repeat("a", 64), false},
		{"池名仅下划线合法", KeyDefaultStoragePool, "_", false},

		// 告警地址：纯空白应被 URL 解析拒绝（有值但没有 scheme/host）
		{"告警地址纯空白拒绝", KeyAlertNotifyURL, "   ", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.key, tc.value)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate(%q, %q) err=%v, wantErr=%v", tc.key, tc.value, err, tc.wantErr)
			}
		})
	}
}

// TestValidatePassword 覆盖口令策略纯函数各分支（改密/建用户/改用户口令共用）。
//
// 风险点：这是「弱口令底线」，策略值来自可被管理员改写的 password_min_length。
// 两条边界最值钱：
//  1. policy <= 0 必须直接放行（否则「关闭策略」反而把所有口令挡在门外）；
//  2. policy 达到 passwordStrongPolicyMin(8) 才额外要求字母+数字——把阈值写错一位
//     （如 >=7）会悄悄放宽/收紧全线口令校验；
//  3. 长度按 Unicode 字符数而非字节数计——中文口令若按字节算会被误判为足够长。
func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name    string
		policy  int
		pwd     string
		wantErr bool
	}{
		{"策略关闭(policy=0)任意口令放行", 0, "a", false},
		{"策略为负(关闭)放行", -1, "", false},
		{"长度不足拒绝", 8, "abc123", true},
		{"长度够但纯数字拒绝", 8, "12345678", true},
		{"长度够但纯字母拒绝", 8, "abcdefgh", true},
		{"长度够但纯符号拒绝", 8, "!@#$%^&*", true},
		{"字母+数字通过", 8, "abc12345", false},
		{"大写字母也算字母", 8, "ABCDEFG1", false},
		{"超过阈值仍要求混合", 10, "abcdefghij", true},
		{"超过阈值混合通过", 10, "abcd1234ef", false},
		// 阈值边界：policy=7 (<8) 只校验长度，不要求混合字符
		{"阈值下方纯数字放行", 7, "1234567", false},
		// Unicode 按字符数计长的关键区分点：policy=3 下 2 个中文（6 字节）应判长度不足
		{"中文口径长度不足拒绝", 3, "中文", true},
		{"中文口令长度足够+混合通过", 8, "中文abc1混合", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.policy, tc.pwd)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidatePassword(%d, %q) err=%v, wantErr=%v", tc.policy, tc.pwd, err, tc.wantErr)
			}
		})
	}

	// 错误文案区分度：长度不足与缺字母/数字必须是不同提示（前端直接回显）
	if err := ValidatePassword(8, "abc"); err == nil || !strings.Contains(err.Error(), "长度") {
		t.Errorf("长度不足应给出含「长度」的提示，实际 %v", err)
	}
	if err := ValidatePassword(8, "12345678"); err == nil || !strings.Contains(err.Error(), "字母") {
		t.Errorf("缺字母应给出含「字母」的提示，实际 %v", err)
	}
}

// newCachedManager 构造一个只带内存缓存、不接 DB 的 Manager（读取路径不触碰 db）。
func newCachedManager(kv map[string]string) *Manager {
	if kv == nil {
		kv = map[string]string{}
	}
	return &Manager{cache: kv}
}

// TestManagerReadDefaults 空缓存时所有读取方法必须回落到与「改造前硬编码」一致的默认值。
//
// 风险点：DefaultStoragePool 兜底值决定「新建 VM 未指定池时落到哪个池」——写错会让
// VM 磁盘落到错误的存储池；SecurityEntrance 默认必须为空（否则默认就把所有人挡在登录外）。
func TestManagerReadDefaults(t *testing.T) {
	m := newCachedManager(nil)
	if got := m.DefaultStoragePool(); got != DefaultStoragePoolFallback {
		t.Errorf("DefaultStoragePool() = %q, want %q", got, DefaultStoragePoolFallback)
	}
	if got := m.Announcement(); got != "" {
		t.Errorf("Announcement() = %q, want 空（无公告）", got)
	}
	if got := m.SecurityEntrance(); got != "" {
		t.Errorf("SecurityEntrance() = %q, want 空（入口关闭）", got)
	}
	if got := m.PasswordMinLength(); got != PasswordMinLengthDefault {
		t.Errorf("PasswordMinLength() = %d, want %d", got, PasswordMinLengthDefault)
	}
	if got := m.VNCTokenTTL(); got != time.Duration(VNCTokenTTLDefaultMin)*time.Minute {
		t.Errorf("VNCTokenTTL() = %s, want %d 分钟", got, VNCTokenTTLDefaultMin)
	}
	if got := m.VNCStale(); got != time.Duration(VNCStaleDefaultMin)*time.Minute {
		t.Errorf("VNCStale() = %s, want %d 分钟", got, VNCStaleDefaultMin)
	}
	if got := m.JumpdCmdBlacklist(); got != "" {
		t.Errorf("JumpdCmdBlacklist() = %q, want 空", got)
	}
	if got := m.AlertNotifyURL(); got != "" {
		t.Errorf("AlertNotifyURL() = %q, want 空", got)
	}
}

// TestManagerCacheReadback 缓存命中时各读取方法返回缓存值，含整数与时长换算。
//
// 风险点：整数配置经 getInt 解析，单位换算（分钟→Duration）写错会让 token 有效期/
// 会话过期时长差 60 倍，属静默放大。
func TestManagerCacheReadback(t *testing.T) {
	m := newCachedManager(map[string]string{
		KeyDefaultStoragePool: "data-pool",
		KeyAnnouncement:       "维护窗口：周六 22:00",
		KeySecurityEntrance:   "secret1",
		KeyPasswordMinLength:  "12",
		KeyVNCTokenTTLMin:     "30",
		KeyVNCStaleMin:        "120",
		KeyJumpdCmdBlacklist:  "rm,-rf",
		KeyAlertNotifyURL:     "https://open.feishu.cn/hook/x",
	})
	if got := m.DefaultStoragePool(); got != "data-pool" {
		t.Errorf("DefaultStoragePool() = %q", got)
	}
	if got := m.Announcement(); got != "维护窗口：周六 22:00" {
		t.Errorf("Announcement() = %q", got)
	}
	if got := m.SecurityEntrance(); got != "secret1" {
		t.Errorf("SecurityEntrance() = %q", got)
	}
	if got := m.PasswordMinLength(); got != 12 {
		t.Errorf("PasswordMinLength() = %d, want 12", got)
	}
	if got := m.VNCTokenTTL(); got != 30*time.Minute {
		t.Errorf("VNCTokenTTL() = %s, want 30m", got)
	}
	if got := m.VNCStale(); got != 120*time.Minute {
		t.Errorf("VNCStale() = %s, want 120m", got)
	}
	if got := m.JumpdCmdBlacklist(); got != "rm,-rf" {
		t.Errorf("JumpdCmdBlacklist() = %q", got)
	}
	if got := m.AlertNotifyURL(); got != "https://open.feishu.cn/hook/x" {
		t.Errorf("AlertNotifyURL() = %q", got)
	}
}

// TestManagerGetStrBoundary 覆盖 GetStr 的两条边界：键缺失返回 def，键存在但值为空串也返回 def。
//
// 风险点：空串必须视同「未设置」——否则公告被设为空串（撤下）后，前端可能拿到空值
// 而非默认，概率性渲染空白；安全入口被设为空串也必须回落到「关闭」而非空口令。
func TestManagerGetStrBoundary(t *testing.T) {
	m := newCachedManager(map[string]string{
		KeyAnnouncement: "", // 存在但为空
	})
	if got := m.GetStr("missing_key", "def"); got != "def" {
		t.Errorf("缺失键应返回 def，得到 %q", got)
	}
	if got := m.GetStr(KeyAnnouncement, "def"); got != "def" {
		t.Errorf("空串值应返回 def，得到 %q", got)
	}
	m.cache["has_val"] = "v"
	if got := m.GetStr("has_val", "def"); got != "v" {
		t.Errorf("非空值应返回自身，得到 %q", got)
	}
}

// TestManagerGetIntInvalidFallsBack 整数配置非法（非数字）时回落到默认值。
//
// 风险点：库中若残留手改的脏值（非数字），getInt 必须回落到安全默认而非 0——
// 0 会让「密码最小长度」变成关闭策略、VNC 会话永不过期，是安全退化。
func TestManagerGetIntInvalidFallsBack(t *testing.T) {
	m := newCachedManager(map[string]string{
		KeyPasswordMinLength: "not-a-number",
		KeyVNCTokenTTLMin:    "not-a-number",
	})
	if got := m.PasswordMinLength(); got != PasswordMinLengthDefault {
		t.Errorf("脏值应回落默认，PasswordMinLength() = %d, want %d", got, PasswordMinLengthDefault)
	}
	if got := m.VNCTokenTTL(); got != time.Duration(VNCTokenTTLDefaultMin)*time.Minute {
		t.Errorf("脏值应回落默认，VNCTokenTTL() = %s", got)
	}
}

// TestAllSnapshotNoAISecrets All() 快照必须包含全部可回显项，且绝不包含 ai_* 三键。
//
// 风险点：All() 的返回直接进 GET /api/settings 响应体。ai_api_key 是服务端保管的
// 出站凭据，一旦被加进快照就会随每个 admin 请求下发（并在前端 JS 里可见），
// 属于「静默泄密」。这条用例把「密钥不进快照」这个约定交给机器守住。
func TestAllSnapshotNoAISecrets(t *testing.T) {
	m := newCachedManager(map[string]string{
		KeyAIBaseURL:          "https://api.deepseek.com/v1",
		KeyAIAPIKey:           "sk-super-secret",
		KeyAIModel:            "deepseek-chat",
		KeyDefaultStoragePool: "data-pool",
		KeyAnnouncement:       "公告",
	})
	all := m.All()

	for _, secret := range []string{KeyAIAPIKey, KeyAIBaseURL, KeyAIModel} {
		if _, ok := all[secret]; ok {
			t.Errorf("All() 快照不应包含 %q（密钥/内部配置不得下发前端）", secret)
		}
	}
	// 应回显的 8 项（含 alert_notify_url、jumpd_cmd_blacklist，见源码注释：admin-only 需回显核对）
	want := []string{
		KeyDefaultStoragePool, KeyVNCTokenTTLMin, KeyVNCStaleMin,
		KeySecurityEntrance, KeyPasswordMinLength, KeyAnnouncement, KeyAlertNotifyURL,
		KeyJumpdCmdBlacklist,
	}
	for _, k := range want {
		if _, ok := all[k]; !ok {
			t.Errorf("All() 快照缺少应回显键 %q", k)
		}
	}
	if got := all[KeyDefaultStoragePool]; got != "data-pool" {
		t.Errorf("快照应回显缓存值，得到 %q", got)
	}
	if got := all[KeyAnnouncement]; got != "公告" {
		t.Errorf("快照应回显公告，得到 %q", got)
	}
	// 变更探测器：键集合发生变化必须显式更新本用例，避免有人顺手把敏感键加进快照
	if len(all) != len(want) {
		t.Errorf("All() 快照键数 = %d, want %d（新增/删除键须同步本用例）", len(all), len(want))
	}
}
