package handler

import "testing"

// TestMaskPassword 掩码规则：≤2 字符定长 ****（防长度泄露），>2 保留前 1 后 1（rune 级，中文不乱码）。
func TestMaskPassword(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空串", "", "****"},
		{"单字符", "a", "****"},
		{"两字符", "ab", "****"},
		{"三字符（最短保留前后）", "abc", "a****c"},
		{"常规口令", "hunter2", "h****2"},
		{"含中文（按 rune 取前后）", "密码测试", "密****试"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := maskPassword(tc.in); got != tc.want {
				t.Errorf("maskPassword(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
