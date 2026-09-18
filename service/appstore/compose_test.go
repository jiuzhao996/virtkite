// compose_test.go 覆盖 Install/Uninstall/Status 的可测部分：
// composeExec 以桩替换（不真跑 docker，真实安装由主协调者 E2E），重点验证
// 目录拷贝语义（保留 data/）、.env 生成、compose 参数形态与 ps 输出解析。
package appstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubCompose 替换 composeExec 桩并记录调用，返回恢复函数。
type composeCall struct {
	dir  string
	key  string
	args []string
}

func stubCompose(t *testing.T, stdout string, err error) func() []composeCall {
	t.Helper()
	orig := composeExec
	var calls []composeCall
	composeExec = func(ctx context.Context, dir, key string, extra ...string) ([]byte, error) {
		calls = append(calls, composeCall{dir: dir, key: key, args: extra})
		if err != nil {
			return nil, err
		}
		return []byte(stdout), nil
	}
	t.Cleanup(func() { composeExec = orig })
	return func() []composeCall { return calls }
}

// writePackage 在临时 catalogDir 里落一个最小应用包，返回各路径。
// fieldsYAML 为版本级 data.yml 的 formFields 段。
func writePackage(t *testing.T, key string, fieldsYAML string) (srcDir, dstDir string) {
	t.Helper()
	srcDir = filepath.Join(catalogDir, key, "1.0.0")
	dstDir = filepath.Join(appsDataDir, key)
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(catalogDir, key, "data.yml"):  "title: 测试应用\ncategory: web\ntags:\n  - t1\n",
		filepath.Join(srcDir, "data.yml"):           "additionalProperties:\n  formFields:\n" + fieldsYAML,
		filepath.Join(srcDir, "docker-compose.yml"): "services:\n  app:\n    image: busybox:1.36\n    ports:\n      - \"${PANEL_APP_PORT}:80\"\n    restart: unless-stopped\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return srcDir, dstDir
}

const portFieldYAML = "    - envKey: PANEL_APP_PORT\n      label: 端口\n      type: number\n      default: 8080\n      required: true\n      rule: paramPort\n"

// isolateDirs 把 catalogDir/appsDataDir 指到临时目录（测试互不污染仓库真实目录）
func isolateDirs(t *testing.T) {
	t.Helper()
	origCatalog, origApps := catalogDir, appsDataDir
	catalogDir = t.TempDir()
	appsDataDir = t.TempDir()
	t.Cleanup(func() { catalogDir, appsDataDir = origCatalog, origApps })
}

func TestInstallSuccess(t *testing.T) {
	isolateDirs(t)
	_, dstDir := writePackage(t, "demo", portFieldYAML)
	calls := stubCompose(t, "", nil)

	appDir, err := Install(context.Background(), "demo", "", map[string]string{"PANEL_APP_PORT": "9000"})
	if err != nil {
		t.Fatalf("Install 失败: %v", err)
	}
	if appDir != dstDir {
		t.Fatalf("返回目录不符: %q != %q", appDir, dstDir)
	}
	// 包文件已拷贝
	for _, name := range []string{"data.yml", "docker-compose.yml"} {
		if _, err := os.Stat(filepath.Join(dstDir, name)); err != nil {
			t.Errorf("包文件 %s 未拷贝: %v", name, err)
		}
	}
	// .env 内容：用户值覆盖默认值
	env, err := os.ReadFile(filepath.Join(dstDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(env) != "PANEL_APP_PORT=9000\n" {
		t.Fatalf(".env 内容不符: %q", env)
	}
	// compose 参数形态：up -d、-p key
	got := calls()
	if len(got) != 1 {
		t.Fatalf("compose 调用次数不符: %d", len(got))
	}
	c := got[0]
	if c.dir != dstDir || c.key != "demo" || len(c.args) != 2 || c.args[0] != "up" || c.args[1] != "-d" {
		t.Fatalf("compose 调用不符: %+v", c)
	}
}

func TestInstallDefaultsFilled(t *testing.T) {
	isolateDirs(t)
	writePackage(t, "demo", portFieldYAML)
	stubCompose(t, "", nil)

	if _, err := Install(context.Background(), "demo", "", nil); err != nil {
		t.Fatalf("无参数安装应走默认值: %v", err)
	}
	env, _ := os.ReadFile(filepath.Join(appsDataDir, "demo", ".env"))
	if string(env) != "PANEL_APP_PORT=8080\n" {
		t.Fatalf("默认值未写入 .env: %q", env)
	}
}

func TestInstallValidationFailsBeforeCopy(t *testing.T) {
	isolateDirs(t)
	writePackage(t, "demo", portFieldYAML)
	calls := stubCompose(t, "", nil)

	_, err := Install(context.Background(), "demo", "", map[string]string{"PANEL_APP_PORT": "70000"})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("期望 ValidationError, got %v", err)
	}
	if len(calls()) != 0 {
		t.Fatal("校验失败不应触发 compose")
	}
	if _, serr := os.Stat(filepath.Join(appsDataDir, "demo")); !os.IsNotExist(serr) {
		t.Fatal("校验失败不应留下拷贝目录")
	}
	// 未知应用
	if _, err := Install(context.Background(), "ghost", "", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未知应用期望 ErrNotFound, got %v", err)
	}
}

func TestInstallKeepsDataDir(t *testing.T) {
	isolateDirs(t)
	writePackage(t, "demo", portFieldYAML)
	stubCompose(t, "", nil)

	dst := filepath.Join(appsDataDir, "demo")
	// 预置已有安装目录与运行数据（模拟重装场景）
	if err := os.MkdirAll(filepath.Join(dst, "data", "html"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldCompose := filepath.Join(dst, "docker-compose.yml")
	if err := os.WriteFile(oldCompose, []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	keepMe := filepath.Join(dst, "data", "html", "index.html")
	if err := os.WriteFile(keepMe, []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(context.Background(), "demo", "", nil); err != nil {
		t.Fatalf("重装失败: %v", err)
	}
	// compose 被覆盖、data/ 数据原样保留
	b, err := os.ReadFile(oldCompose)
	if err != nil || string(b) == "旧内容" {
		t.Fatalf("compose 未被覆盖: %q %v", b, err)
	}
	data, err := os.ReadFile(keepMe)
	if err != nil || string(data) != "<h1>hi</h1>" {
		t.Fatalf("data/ 运行数据丢失: %q %v", data, err)
	}
}

func TestInstallComposeFailure(t *testing.T) {
	isolateDirs(t)
	writePackage(t, "demo", portFieldYAML)
	boom := &ComposeError{Command: "docker compose up", Stderr: "port is already allocated", Err: errors.New("exit status 1")}
	stubCompose(t, "", boom)

	_, err := Install(context.Background(), "demo", "", nil)
	var ce *ComposeError
	if !errors.As(err, &ce) {
		t.Fatalf("期望 ComposeError, got %v", err)
	}
	if !strings.Contains(err.Error(), "port is already allocated") {
		t.Fatalf("错误应携带 stderr 摘要: %v", err)
	}
}

func TestUninstall(t *testing.T) {
	t.Run("未安装且不删数据报未安装", func(t *testing.T) {
		isolateDirs(t)
		stubCompose(t, "", nil)
		if err := Uninstall("ghost", false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("期望 ErrNotFound, got %v", err)
		}
	})
	t.Run("未安装且删数据静默清理", func(t *testing.T) {
		isolateDirs(t)
		calls := stubCompose(t, "", nil)
		// 残留目录（无 compose 文件）
		if err := os.MkdirAll(filepath.Join(appsDataDir, "ghost", "data"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Uninstall("ghost", true); err != nil {
			t.Fatalf("期望静默清理, got %v", err)
		}
		if len(calls()) != 0 {
			t.Fatal("无 compose 文件不应触发 compose")
		}
		if _, serr := os.Stat(filepath.Join(appsDataDir, "ghost")); !os.IsNotExist(serr) {
			t.Fatal("残留目录未被清理")
		}
	})
	t.Run("仅 down 保留目录", func(t *testing.T) {
		isolateDirs(t)
		writePackage(t, "demo", portFieldYAML)
		stubCompose(t, "", nil)
		// 先真实走一遍 Install 产生安装目录（compose+yml+.env），再验证卸载语义
		if _, err := Install(context.Background(), "demo", "", nil); err != nil {
			t.Fatalf("预安装失败: %v", err)
		}
		calls := stubCompose(t, "", nil)
		if err := Uninstall("demo", false); err != nil {
			t.Fatalf("卸载失败: %v", err)
		}
		got := calls()
		if len(got) != 1 || got[0].args[0] != "down" || len(got[0].args) != 1 {
			t.Fatalf("down 参数不符: %+v", got)
		}
		if _, serr := os.Stat(filepath.Join(appsDataDir, "demo", ".env")); serr != nil {
			t.Fatal("保留模式目录应原样存在")
		}
	})
	t.Run("removeData 连目录一起删", func(t *testing.T) {
		isolateDirs(t)
		writePackage(t, "demo", portFieldYAML)
		stubCompose(t, "", nil)
		if _, err := Install(context.Background(), "demo", "", nil); err != nil {
			t.Fatalf("预安装失败: %v", err)
		}
		// .env 缺失场景：卸载前应补空文件让 compose 能解析（Install 刚写过，这里删掉制造缺失）
		if err := os.Remove(filepath.Join(appsDataDir, "demo", ".env")); err != nil {
			t.Fatal(err)
		}
		calls := stubCompose(t, "", nil)
		if err := Uninstall("demo", true); err != nil {
			t.Fatalf("卸载失败: %v", err)
		}
		got := calls()
		if len(got) != 1 || got[0].args[0] != "down" || got[0].args[1] != "-v" {
			t.Fatalf("down -v 参数不符: %+v", got)
		}
		if _, serr := os.Stat(filepath.Join(appsDataDir, "demo")); !os.IsNotExist(serr) {
			t.Fatal("removeData 后目录应被删除")
		}
	})
	t.Run("down 失败保留目录", func(t *testing.T) {
		isolateDirs(t)
		writePackage(t, "demo", portFieldYAML)
		stubCompose(t, "", nil)
		if _, err := Install(context.Background(), "demo", "", nil); err != nil {
			t.Fatalf("预安装失败: %v", err)
		}
		boom := &ComposeError{Command: "docker compose down", Stderr: "daemon down", Err: errors.New("exit status 1")}
		stubCompose(t, "", boom)
		var ce *ComposeError
		if err := Uninstall("demo", true); !errors.As(err, &ce) || !strings.Contains(err.Error(), "daemon down") {
			t.Fatalf("期望透出 ComposeError, got %v", err)
		}
		if _, serr := os.Stat(filepath.Join(appsDataDir, "demo")); serr != nil {
			t.Fatal("down 失败时目录不应被删（先 down 成功再删目录的顺序保证）")
		}
	})
}

func TestStatus(t *testing.T) {
	isolateDirs(t)
	writePackage(t, "demo", portFieldYAML)
	// 先 Install 产生安装目录（stub 掉 up），再验证 Status 的 ps 解析
	stubCompose(t, "", nil)
	if _, err := Install(context.Background(), "demo", "", nil); err != nil {
		t.Fatalf("预安装失败: %v", err)
	}
	// 无 compose 文件的目录不算已装应用
	if err := os.MkdirAll(filepath.Join(appsDataDir, "junk"), 0o755); err != nil {
		t.Fatal(err)
	}
	psJSON := `[{"Service":"app","State":"running"},{"Service":"app","State":"exited"}]`
	calls := stubCompose(t, psJSON, nil)

	items, err := Status()
	if err != nil {
		t.Fatalf("Status 失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应只有 1 个已装应用: %+v", items)
	}
	st := items[0]
	if st.Name != "demo" || st.Services != 2 || st.Running != 1 {
		t.Fatalf("状态统计不符: %+v", st)
	}
	got := calls()
	if len(got) != 1 || got[0].args[0] != "ps" {
		t.Fatalf("ps 调用不符: %+v", got)
	}
}

func TestStatusEmptyAndDaemonDown(t *testing.T) {
	isolateDirs(t)
	items, err := Status()
	if err != nil || len(items) != 0 {
		t.Fatalf("无安装目录应得空数组, got %+v, %v", items, err)
	}

	writePackage(t, "demo", portFieldYAML)
	stubCompose(t, "", nil)
	if _, err := Install(context.Background(), "demo", "", nil); err != nil {
		t.Fatalf("预安装失败: %v", err)
	}
	// daemon 不可达（composeExec 报错）：不整体报错，该应用计 0
	stubCompose(t, "", errors.New("Cannot connect to the Docker daemon"))
	items, err = Status()
	if err != nil {
		t.Fatalf("daemon 不可达不应整体报错: %v", err)
	}
	if len(items) != 1 || items[0].Services != 0 || items[0].Running != 0 {
		t.Fatalf("daemon 不可达应计 0: %+v", items)
	}
}

func TestParseComposePs(t *testing.T) {
	tests := []struct {
		name             string
		out              string
		services, runing int
		wantErr          bool
	}{
		{name: "JSON 数组格式", out: `[{"State":"running"},{"State":"exited"},{"State":"running"}]`, services: 3, runing: 2},
		{name: "JSON Lines 格式", out: "{\"State\":\"running\"}\n{\"State\":\"running\"}\n", services: 2, runing: 2},
		{name: "空输出", out: "", services: 0, runing: 0},
		{name: "空白输出", out: "  \n", services: 0, runing: 0},
		{name: "坏 JSON 报错", out: "not json", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			services, running, err := parseComposePs([]byte(tt.out))
			if tt.wantErr {
				if err == nil {
					t.Fatal("期望报错")
				}
				return
			}
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if services != tt.services || running != tt.runing {
				t.Fatalf("统计不符: got (%d,%d), want (%d,%d)", services, running, tt.services, tt.runing)
			}
		})
	}
}

func TestTailString(t *testing.T) {
	if got := tailString("  abc  ", 10); got != "abc" {
		t.Fatalf("短串 trim 不符: %q", got)
	}
	if got := tailString("abcdef", 3); got != "def" {
		t.Fatalf("截尾不符: %q", got)
	}
}

func TestEnvFileContent(t *testing.T) {
	fields := []FormField{
		{EnvKey: "PANEL_A", Label: "a"},
		{EnvKey: "PANEL_B", Label: "b"},
	}
	got := envFileContent(fields, map[string]string{"PANEL_A": "1", "PANEL_B": ""})
	if got != "PANEL_A=1\nPANEL_B=\n" {
		t.Fatalf(".env 内容不符: %q", got)
	}
}
