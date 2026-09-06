package handler

import "testing"

// TestEscapePromLabel PromQL 字符串字面量转义（vm 标签匹配值）。
func TestEscapePromLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"node1", "node1"},            // 普通名不变
		{`back"slash`, `back\"slash`}, // 双引号转义
		{`back\slash`, `back\\slash`}, // 反斜杠转义
		{"a\nb", `a\nb`},              // 换行转义
	}
	for _, c := range cases {
		if got := escapePromLabel(c.in); got != c.want {
			t.Errorf("escapePromLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestClampMinutes minutes 参数钳制（5~360）。
func TestClampMinutes(t *testing.T) {
	cases := []struct {
		in   string
		def  int
		want int
	}{
		{"", 60, 60},      // 空回默认
		{"abc", 30, 30},   // 非法回默认
		{"3", 60, 60},     // 低于下界回默认
		{"10", 60, 10},    // 合法
		{"9999", 60, 360}, // 超上界钳制
	}
	for _, c := range cases {
		if got := clampMinutes(c.in, c.def); got != c.want {
			t.Errorf("clampMinutes(%q, %d) = %d, want %d", c.in, c.def, got, c.want)
		}
	}
}
