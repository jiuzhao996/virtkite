package handler

import (
	"testing"
)

// TestParsePlaybookHeader 覆盖 playbook 头部元数据行解析。
//
// 风险点：清单页的名称/说明/目标全靠这一行——解析漂移会让「内置种子」在 UI 上
// 退化成裸文件名；同时解析必须只看前 8 行，恶意构造的深层注释不应被当元数据。
func TestParsePlaybookHeader(t *testing.T) {
	raw := []byte("# vmops-playbook: name=init-node | desc=装机初始化 | targets=linux\n---\n- name: x\n")
	name, desc, targets, vars := parsePlaybookHeader(raw)
	if name != "init-node" || desc != "装机初始化" || targets != "linux" {
		t.Errorf("三段元数据解析不符: %q %q %q", name, desc, targets)
	}
	if len(vars) != 0 {
		t.Errorf("无 vars 声明应得空切片: %v", vars)
	}

	// 只取第一条匹配（后续行同形态不算数）
	raw2 := []byte("# vmops-playbook: name=first\n---\n# vmops-playbook: name=second\n")
	if n, _, _, _ := parsePlaybookHeader(raw2); n != "first" {
		t.Errorf("应只取第一条元数据行: %q", n)
	}

	// 无元数据行 → 全空（列表页回退用文件 id 当名称）
	if n, d, tg, _ := parsePlaybookHeader([]byte("---\n- name: x\n")); n != "" || d != "" || tg != "" {
		t.Errorf("无元数据行应返回空串: %q %q %q", n, d, tg)
	}

	// 元数据行在 8 行之后 → 不识别
	deep := []byte("# a\n# b\n# c\n# d\n# e\n# f\n# g\n# h\n# vmops-playbook: name=late\n---\n")
	if n, _, _, _ := parsePlaybookHeader(deep); n != "" {
		t.Errorf("超过头部窗口的元数据行不应识别: %q", n)
	}
}

// TestPlaybookIDRe 覆盖 id 白名单：路径穿越/斜杠/空首字符一律拒绝。
func TestPlaybookIDRe(t *testing.T) {
	valid := []string{"init-node", "a", "A1_b-c", "x9"}
	for _, id := range valid {
		if !playbookIDRe.MatchString(id) {
			t.Errorf("%q 应合法", id)
		}
	}
	invalid := []string{"", "../evil", "a/b", "-lead", ".hidden", "x y", "toolong" + string(make([]byte, 0)) + strings_repeat(70)}
	for _, id := range invalid {
		if playbookIDRe.MatchString(id) {
			t.Errorf("%q 应非法", id)
		}
	}
}

// strings_repeat 生成超长串（70 字符，超 64 上限）。
func strings_repeat(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}
