package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
)

// TestParseRecap 覆盖 PLAY RECAP 段解析。
//
// 风险点：解析结果直接进任务 Result 的 recap 字段，前端按主机渲染 ok/changed/failed
// 矩阵——解析错一台，矩阵就整体失真；同时 ansible 输出有大量装饰行（ cowsay 对话框、
// 空行、PLAY 横幅），解析必须只认「host : ok=N」形态。
func TestParseRecap(t *testing.T) {
	text := strings.Join([]string{
		"PLAY RECAP",
		"e2e-web4                   : ok=5    changed=4    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0",
		"web-2                      : ok=1    changed=0    unreachable=1    failed=2    skipped=0    rescued=0    ignored=0",
	}, "\n")
	got := parseRecap(text)
	if len(got) != 2 {
		t.Fatalf("应解析出 2 台主机，得到 %d: %#v", len(got), got)
	}
	c := got["e2e-web4"]
	if c["ok"] != 5 || c["changed"] != 4 || c["failed"] != 0 {
		t.Errorf("e2e-web4 计数不符: %#v", c)
	}
	c2 := got["web-2"]
	if c2["ok"] != 1 || c2["unreachable"] != 1 || c2["failed"] != 2 {
		t.Errorf("web-2 计数不符: %#v", c2)
	}

	// 非 recap 行（横幅/空行/无 ok= 的行）一律忽略
	if got := parseRecap("PLAY [init] ************\n\nTASK [x] ***********"); len(got) != 0 {
		t.Errorf("装饰行不应被解析: %#v", got)
	}
	if got := parseRecap(""); len(got) != 0 {
		t.Errorf("空输入应得空表: %#v", got)
	}
}

// TestUintSliceParam 覆盖 targets 参数解析：json 反序列化的数字是 float64，
// 必须无损转 uint；负数/非整型/缺键都要拒绝而非静默截断。
func TestUintSliceParam(t *testing.T) {
	got, err := uintSliceParam(map[string]interface{}{"targets": []interface{}{float64(89), float64(90)}}, "targets")
	if err != nil || len(got) != 2 || got[0] != 89 || got[1] != 90 {
		t.Errorf("合法 float64 列表应无损转换: %v, %v", got, err)
	}
	if _, err := uintSliceParam(map[string]interface{}{"targets": []interface{}{float64(-1)}}, "targets"); err == nil {
		t.Error("负数应拒绝")
	}
	if _, err := uintSliceParam(map[string]interface{}{"targets": []interface{}{"89"}}, "targets"); err == nil {
		t.Error("字符串元素应拒绝")
	}
	if _, err := uintSliceParam(map[string]interface{}{}, "targets"); err == nil {
		t.Error("缺键应拒绝")
	}
}

// TestAnsibleSSHUser 免密通道登录用户的取值。
//
// 风险点：这里曾硬编码 root，而平台公钥是按 cloud-init 配的 user 落到那个用户家目录的。
// 用户填 admin/rocky 时，以 root 登录会撞上镜像 root 的 forced-command 提示，
// 提示语污染模块 stdout，ansible 报「Module result deserialization failed」——
// 症状与原因完全不相干，排查要花很久。本测试把「公钥落点 == 登录用户」这条不变量钉死。
func TestAnsibleSSHUser(t *testing.T) {
	cases := []struct {
		name string
		vm   model.VM
		want string
	}{
		{"公钥落 admin 家目录就用 admin", model.VM{AnsibleUser: "admin", AnsibleReady: true}, "admin"},
		{"公钥落 rocky 家目录就用 rocky", model.VM{AnsibleUser: "rocky", AnsibleReady: true}, "rocky"},
		{"空白值回落 root（存量行）", model.VM{AnsibleUser: "   "}, "root"},
		{"空值回落 root（字段引入前的数据）", model.VM{}, "root"},
	}
	for _, tc := range cases {
		if got := ansibleSSHUser(tc.vm); got != tc.want {
			t.Errorf("%s: ansibleSSHUser=%q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestWriteRunAnsibleConfigAbsolutePaths 生成的 ansible.cfg 里 roles_path / fact 缓存
// 必须是绝对路径。
//
// 风险点：ansible 解析配置文件里的相对路径是按「配置文件所在目录」算的，而 run 级
// ansible.cfg 就放在 run 目录里、DATA_DIR 默认又是相对的 "data"——于是
// `roles_path = data/ansible/roles` 会被解析成 <run 目录>/data/ansible/roles，
// 共享 role 直接「not found」，fact 缓存也静默写到别处。实测踩过一次。
func TestWriteRunAnsibleConfigAbsolutePaths(t *testing.T) {
	old := config.GlobalConfig
	dir := t.TempDir()
	config.GlobalConfig = &config.Config{DataDir: dir}
	defer func() { config.GlobalConfig = old }()

	// 故意用相对 DATA_DIR，复现真实部署（.env 里 DATA_DIR 缺省即 "data"）
	config.GlobalConfig.DataDir = "data"

	runDir := t.TempDir()
	path := writeRunAnsibleConfig(runDir)
	if path == "" {
		t.Fatal("未生成 ansible.cfg")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)

	rolesLine := ""
	cacheLine := ""
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "roles_path") {
			rolesLine = line
		}
		if strings.HasPrefix(line, "fact_caching_connection") {
			cacheLine = line
		}
	}
	if rolesLine == "" || cacheLine == "" {
		t.Fatalf("cfg 缺 roles_path / fact_caching_connection：\n%s", content)
	}
	for _, line := range []string{rolesLine, cacheLine} {
		_, val, _ := strings.Cut(line, "=")
		val = strings.TrimSpace(val)
		if !filepath.IsAbs(val) {
			t.Errorf("配置项必须是绝对路径，实际 %q（%s）", val, line)
		}
	}
}
