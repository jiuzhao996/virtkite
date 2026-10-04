package ansible

import (
	"io"
	"log"
	"os"
	"strings"
	"testing"
)

// TestMain 丢弃日志输出（探测失败的候选路径提示等不混进 go test -v 输出）。
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	code := m.Run()
	log.SetOutput(os.Stderr)
	os.Exit(code)
}

// TestBuildInventory 覆盖 inventory 生成：条目格式、口令内单引号转义、分组引用
// 过滤、空主机拒绝。
//
// 风险点：该文件是执行引擎的唯一输入（0700 目录 + 0600 文件 + 跑完即删的瞬时口令
// 载体）——格式错一处整批执行就废；口令含单引号时若不做 ini 转义，轻则该主机认证
// 失败，重则口令后半段被 shell 当命令。
func TestBuildInventory(t *testing.T) {
	dir := t.TempDir()
	hosts := []HostEntry{
		{Name: "web-1", IP: "10.0.0.78", User: "root", Port: 22, Pass: "p@ss'1 2"},
		{Name: "web-2", IP: "10.0.0.84", User: "ubuntu", Port: 2222, Pass: "plain"},
	}
	inv, err := BuildInventory(dir, hosts, map[string][]string{
		"web":  {"web-1", "web-2"},
		"ghost": {"web-1", "not-exist"}, // 引用未定义主机应被过滤
		"all":  {"web-1"},               // 内置组名不应产出重复段
	})
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}
	raw, err := os.ReadFile(inv)
	if err != nil {
		t.Fatalf("读 inventory: %v", err)
	}
	text := string(raw)

	// 主机行：名字/IP/用户/端口/口令全量呈现，单引号口令已转义
	if !strings.Contains(text, "web-1 ansible_host=10.0.0.78 ansible_port=22 ansible_user=root ansible_ssh_pass='p@ss'\"'\"'1 2'") {
		t.Errorf("web-1 行不符合预期（含单引号转义）:\n%s", text)
	}
	if !strings.Contains(text, "web-2 ansible_host=10.0.0.84 ansible_port=2222 ansible_user=ubuntu ansible_ssh_pass='plain'") {
		t.Errorf("web-2 行不符合预期:\n%s", text)
	}
	// 分组：合法组输出、幽灵成员过滤、all 组跳过
	if !strings.Contains(text, "[web]\nweb-1\nweb-2\n") {
		t.Errorf("[web] 组缺失或成员不符:\n%s", text)
	}
	if strings.Contains(text, "not-exist") {
		t.Errorf("未定义主机 not-exist 不应出现在任何组:\n%s", text)
	}
	if strings.Count(text, "[all]") != 1 {
		t.Errorf("[all] 段应只有初始定义的一处:\n%s", text)
	}

	// 空主机列表必须拒绝（执行引擎拿到空 inventory 会整批无事发生还报成功）
	if _, err := BuildInventory(dir, nil, nil); err == nil {
		t.Error("空主机列表应返回错误")
	}
}

// TestSyntaxCheckRules 覆盖语法校验的纯规则段（空内容 / 缺 --- 分隔符 /
// 元数据注释行在 --- 前属合法惯例）。真实 --syntax-check 实跑用例在检测到
// 宿主机引擎存在时补一条，无引擎则跳过（CI 环境可能没装 ansible）。
func TestSyntaxCheckRules(t *testing.T) {
	if err := SyntaxCheck(""); err == nil {
		t.Error("空内容应报错")
	}
	if err := SyntaxCheck("- name: x\n  hosts: all"); err == nil {
		t.Error("缺 --- 分隔符应报错")
	}
	// 引擎不存在的环境跳过实跑（前两条规则在 Detect 之前，上面已覆盖）
	if _, err := Detect(); err != nil {
		t.Skip("宿主机未安装 ansible，跳过 --syntax-check 实跑用例")
	}

	good := "# vmops-playbook: name=x | desc=y | targets=linux\n---\n- name: ok\n  hosts: all\n  tasks:\n    - name: t\n      ansible.builtin.command: hostname\n      changed_when: false\n"
	if err := SyntaxCheck(good); err != nil {
		t.Errorf("合法 playbook（注释头在 --- 前）不应报错: %v", err)
	}
	bad := "---\n- name: bad\n  hosts: all\n  tasks:\n    - name: t\n      ansible.builtin.command: hostname\n     broken_indent: true\n"
	if err := SyntaxCheck(bad); err == nil {
		t.Error("缩进损坏的 playbook 应报错")
	}
}
