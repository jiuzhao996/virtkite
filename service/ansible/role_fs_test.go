package ansible

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffoldRole 新建共享 role 的标准骨架。
//
// 风险点：骨架里 tasks/main.yml 的头部 `# vmops-role:` 行是清单页描述的唯一来源，
// 占位符替换漏一处，UI 上就会显示成裸文件名或 {{desc}} 字面量；另外同名冲突必须拒
// （否则会把已有 role 的 tasks/main.yml 覆盖掉，用户写到一半的任务直接没了）。
func TestScaffoldRole(t *testing.T) {
	base := t.TempDir()
	if err := ScaffoldRole(base, "demo-role", "演示 role"); err != nil {
		t.Fatalf("ScaffoldRole: %v", err)
	}
	for _, rel := range []string{
		"tasks/main.yml", "handlers/main.yml", "defaults/main.yml",
		"vars/main.yml", "meta/main.yml", "README.md",
	} {
		if _, err := os.Stat(filepath.Join(base, "demo-role", filepath.FromSlash(rel))); err != nil {
			t.Errorf("骨架缺文件 %s: %v", rel, err)
		}
	}
	for _, d := range []string{"templates", "files"} {
		if st, err := os.Stat(filepath.Join(base, "demo-role", d)); err != nil || !st.IsDir() {
			t.Errorf("骨架缺目录 %s", d)
		}
	}

	raw, err := os.ReadFile(filepath.Join(base, "demo-role", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "{{") {
		t.Errorf("占位符未被替换：%s", s[:120])
	}
	if !strings.Contains(s, "# vmops-role: name=demo-role | desc=演示 role") {
		t.Errorf("头部元数据行不符：%q", strings.SplitN(s, "\n", 2)[0])
	}

	// 同名冲突
	if err := ScaffoldRole(base, "demo-role", "再来一次"); err == nil {
		t.Error("同名 role 未被拒")
	}
	// 非法名
	for _, bad := range []string{"../evil", "-lead", "a b", ""} {
		if err := ScaffoldRole(base, bad, "x"); err == nil {
			t.Errorf("非法 role 名 %q 未被拒", bad)
		}
	}
	// 描述留空要有兜底（否则头部会写成 desc= 空）
	if err := ScaffoldRole(base, "nodesc", "  "); err != nil {
		t.Fatalf("空描述应被兜底而非报错: %v", err)
	}
}

// TestListRoles 清单：描述来自头部元数据行，文件计数正确，非目录/非法名被跳过。
func TestListRoles(t *testing.T) {
	base := t.TempDir()
	if err := ScaffoldRole(base, "role-b", "B 描述"); err != nil {
		t.Fatal(err)
	}
	if err := ScaffoldRole(base, "role-a", "A 描述"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "loose.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := ListRoles(base)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("条目数=%d，期望 2（非目录应跳过）：%+v", len(items), items)
	}
	if items[0].Name != "role-a" || items[1].Name != "role-b" {
		t.Errorf("未按名字排序：%v %v", items[0].Name, items[1].Name)
	}
	if items[0].Desc != "A 描述" {
		t.Errorf("描述未从头部元数据行解析：%q", items[0].Desc)
	}
	if items[0].FileCount != 6 {
		t.Errorf("文件数=%d，期望 6（骨架文件数）", items[0].FileCount)
	}

	// 目录不存在时返回空而非报错（首次启动、库还没建）
	if got, err := ListRoles(filepath.Join(base, "nope")); err != nil || len(got) != 0 {
		t.Errorf("目录不存在时应返回空，实际 items=%v err=%v", got, err)
	}
}

// TestRemoveDir 删除守卫：非法名不落到 RemoveAll，不存在的目录报错而不是假成功。
//
// 风险点：RemoveAll 对不存在的路径返回 nil（静默成功），且名字不过白名单的话
// `..` 之类会把删除指向库外面。
func TestRemoveDir(t *testing.T) {
	base := t.TempDir()
	if err := ScaffoldRole(base, "victim", "待删"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDir(base, "victim"); err != nil {
		t.Fatalf("RemoveDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "victim")); err == nil {
		t.Error("目录未被删除")
	}
	if err := RemoveDir(base, "victim"); err == nil {
		t.Error("删除不存在的 role 应报错（不能假成功）")
	}
	for _, bad := range []string{"..", "../..", "a/b", ""} {
		if err := RemoveDir(base, bad); err == nil {
			t.Errorf("非法名 %q 未被拒", bad)
		}
	}
	// 库外文件必须毫发无损
	outside := filepath.Join(filepath.Dir(base), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = RemoveDir(base, "../outside.txt")
	if _, err := os.Stat(outside); err != nil {
		t.Error("越界删除动到了库外文件")
	}
}

// TestSyntaxCheckRole 共享 role 的等价语法校验：引用了 role 的最小 playbook 能过，
// 写坏 role 的 YAML 要报错。
func TestSyntaxCheckRole(t *testing.T) {
	if _, err := Detect(); err != nil {
		t.Skipf("宿主机未安装 ansible，跳过：%v", err)
	}
	base := t.TempDir()
	if err := ScaffoldRole(base, "good-role", "好的"); err != nil {
		t.Fatal(err)
	}
	if err := SyntaxCheckRole(base, "good-role"); err != nil {
		t.Fatalf("合法 role 被拒: %v", err)
	}

	if err := ScaffoldRole(base, "bad-role", "坏的"); err != nil {
		t.Fatal(err)
	}
	// 写坏 tasks/main.yml：YAML 结构非法
	if err := WriteDirFile(base, "bad-role", "tasks/main.yml", "---\n- name: x\n   bad-indent: [unclosed\n"); err != nil {
		t.Fatal(err)
	}
	if err := SyntaxCheckRole(base, "bad-role"); err == nil {
		t.Error("写坏的 role 未被校验拦住")
	} else if !strings.Contains(err.Error(), "role 校验未通过") {
		t.Errorf("错误文案不符: %v", err)
	}

	if err := SyntaxCheckRole(base, "no-such-role"); err == nil {
		t.Error("不存在的 role 应报错")
	}
	if err := SyntaxCheckRole(base, "../evil"); err == nil {
		t.Error("非法名应报错")
	}
}
