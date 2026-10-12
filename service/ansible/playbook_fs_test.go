package ansible

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedDirTree 造一个目录形态 playbook 的样例树（site.yml + roles/nginx/…）。
func seedDirTree(t *testing.T, base string) {
	t.Helper()
	files := map[string]string{
		"web-stack/site.yml":                            "---\n- hosts: all\n  roles: [nginx]\n",
		"web-stack/roles/nginx/tasks/main.yml":          "---\n- name: t\n  ansible.builtin.command: /bin/true\n",
		"web-stack/roles/nginx/handlers/main.yml":       "---\n- name: h\n  ansible.builtin.command: /bin/true\n",
		"web-stack/roles/nginx/templates/index.html.j2": "<h1>{{ ansible_hostname }}</h1>\n",
	}
	for rel, content := range files {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

// TestResolvePlaybook 解析优先级：单文件 <id>.yml > 目录 <id>/site.yml > 目录内首个 *.yml。
//
// 风险点：ResolvePlaybook 是「playbook 到底指哪个文件」的唯一判定点，handler 的
// 读/写/执行、executor 的路径拼装全依赖它；判错一处，轻则编辑了 A 文件却执行 B，
// 重则目录形态 playbook 被写成「目录旁的 <id>.yml」——roles 引用随即失效。
func TestResolvePlaybook(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "single.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedDirTree(t, base)
	// 目录形态但没有 site.yml：退化为目录内首个 *.yml
	onlyDir := filepath.Join(base, "no-site")
	if err := os.MkdirAll(onlyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(onlyDir, "main.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 同名时单文件优先
	if err := os.MkdirAll(filepath.Join(base, "single"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "single", "site.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		id        string
		wantBase  string
		wantIsDir bool
		wantOK    bool
	}{
		{"single", "single.yml", false, true},           // 单文件优先于同名目录
		{"web-stack", "web-stack/site.yml", true, true}, // site.yml 为主文件
		{"no-site", "no-site/main.yml", true, true},     // 退化到首个 yml
		{"missing", "", false, false},
		{"../etc", "", false, false}, // 白名单外
		{"a b", "", false, false},    // 空格非法
		{"", "", false, false},       // 空 id
	}
	for _, tc := range cases {
		got, isDir, ok := ResolvePlaybook(base, tc.id)
		if ok != tc.wantOK {
			t.Errorf("ResolvePlaybook(%q) ok=%v, want %v", tc.id, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if isDir != tc.wantIsDir {
			t.Errorf("ResolvePlaybook(%q) isDir=%v, want %v", tc.id, isDir, tc.wantIsDir)
		}
		want := filepath.Join(base, filepath.FromSlash(tc.wantBase))
		if got != want {
			t.Errorf("ResolvePlaybook(%q)=%q, want %q", tc.id, got, want)
		}
	}
}

// TestSafePlaybookPath 目录逃逸守卫：任何越出 playbook 根目录的相对路径都必须被拒。
//
// 风险点：这是本文件唯一的路径注入面（rel 直接来自 HTTP query/body），漏一个
// `..` 变体就等于把整个 data/ 目录暴露成可写。
func TestSafePlaybookPath(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "pb")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		"../escape.yml",
		"roles/../../escape.yml",
		"..",
		"../",
		"a/../../../b",
	}
	for _, rel := range bad {
		if _, err := safeDirPath(base, "pb", rel); err == nil {
			t.Errorf("safeDirPath(%q) 未拒绝越界路径", rel)
		}
	}
	good := map[string]string{
		"site.yml":               "site.yml",
		"roles/x/tasks/main.yml": "roles/x/tasks/main.yml",
		"./site.yml":             "site.yml",
		"roles/../site.yml":      "site.yml", // 词法上仍落在根内，允许
	}
	for rel, want := range good {
		got, err := safeDirPath(base, "pb", rel)
		if err != nil {
			t.Errorf("safeDirPath(%q) 意外失败: %v", rel, err)
			continue
		}
		if got != filepath.Join(root, filepath.FromSlash(want)) {
			t.Errorf("safeDirPath(%q)=%q, want %q", rel, got, filepath.Join(root, filepath.FromSlash(want)))
		}
	}
	// id 本身非法时一并拒绝（防止 base/id 拼接被污染）
	if _, err := safeDirPath(base, "../pb", "site.yml"); err == nil {
		t.Error("safePlaybookPath 未拒绝非法 id")
	}
}

// TestPlaybookFileCRUD 文件增删改查 + 递归清单。
func TestPlaybookFileCRUD(t *testing.T) {
	base := t.TempDir()
	seedDirTree(t, base)

	files, err := ListDirFiles(base, "web-stack")
	if err != nil {
		t.Fatalf("ListPlaybookFiles: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("文件数=%d, want 4（%+v）", len(files), files)
	}
	// 排序稳定（路径字典序）
	for i := 1; i < len(files); i++ {
		if files[i-1].Path > files[i].Path {
			t.Fatalf("清单未按路径排序: %q > %q", files[i-1].Path, files[i].Path)
		}
	}

	// 写新文件（自动建父目录）
	if err := WriteDirFile(base, "web-stack", "roles/nginx/vars/main.yml", "---\nport: 80\n"); err != nil {
		t.Fatalf("WritePlaybookFile: %v", err)
	}
	got, err := ReadDirFile(base, "web-stack", "roles/nginx/vars/main.yml")
	if err != nil {
		t.Fatalf("ReadPlaybookFile: %v", err)
	}
	if got != "---\nport: 80\n" {
		t.Errorf("读回内容=%q", got)
	}

	// 越界写必须被拒
	if err := WriteDirFile(base, "web-stack", "../evil.yml", "x"); err == nil {
		t.Error("WritePlaybookFile 未拒绝越界路径")
	}
	if _, err := os.Stat(filepath.Join(base, "evil.yml")); err == nil {
		t.Error("越界写实际落盘了")
	}

	// 根目录不可删
	if err := DeleteDirFile(base, "web-stack", "."); err == nil {
		t.Error("DeletePlaybookFile 未拒绝删除根目录")
	}
	// 删文件
	if err := DeleteDirFile(base, "web-stack", "roles/nginx/vars/main.yml"); err != nil {
		t.Fatalf("DeletePlaybookFile: %v", err)
	}
	if _, err := ReadDirFile(base, "web-stack", "roles/nginx/vars/main.yml"); err == nil {
		t.Error("删除后仍可读到文件")
	}
}

// TestIsDirPlaybook 目录判定。
func TestIsDirPlaybook(t *testing.T) {
	base := t.TempDir()
	seedDirTree(t, base)
	if err := os.WriteFile(filepath.Join(base, "single.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsDirPlaybook(base, "web-stack") {
		t.Error("web-stack 应为目录形态")
	}
	if IsDirPlaybook(base, "single") {
		t.Error("single 应为单文件形态")
	}
	if IsDirPlaybook(base, "nope") {
		t.Error("不存在的 id 不应判为目录")
	}
}

// TestSyntaxCheckContentInDir 目录形态内容预检：roles 相对引用可见时通过，主文件
// 写成「引用不存在的 role」时必须报错（否则保存点会放行一个跑不起来的 playbook）。
func TestSyntaxCheckContentInDir(t *testing.T) {
	eng, err := Detect()
	if err != nil {
		t.Skipf("宿主机未安装 ansible，跳过：%v", err)
	}
	base := t.TempDir()
	seedDirTree(t, base)
	main := filepath.Join(base, "web-stack", "site.yml")

	okContent := "---\n- hosts: all\n  roles:\n    - nginx\n"
	if err := SyntaxCheckContentInDir(main, okContent); err != nil {
		t.Fatalf("合法内容被拒: %v", err)
	}
	badContent := "---\n- hosts: all\n  roles:\n    - no-such-role\n"
	if err := SyntaxCheckContentInDir(main, badContent); err == nil {
		t.Fatal("引用不存在 role 的内容未被拒")
	} else if !strings.Contains(err.Error(), "语法") && !strings.Contains(err.Error(), "role") {
		t.Errorf("错误文案未指向语法/role 问题: %v", err)
	}
	// 校验用的临时目录不得污染源目录：源主文件内容必须原样
	raw, rerr := os.ReadFile(main)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(raw) != "---\n- hosts: all\n  roles: [nginx]\n" {
		t.Errorf("源主文件被校验过程改写: %q", raw)
	}
	_ = eng
}
