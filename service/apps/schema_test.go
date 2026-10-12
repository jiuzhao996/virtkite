package apps

import (
	"regexp"
	"strings"
	"testing"
)

// testApp 一份覆盖全部控件类型的应用 schema，供校验/渲染用例复用。
func testApp() App {
	return App{
		ID:   "demo",
		Name: "Demo",
		Fields: []Field{
			{Key: "NAME", Label: "名称", Type: FieldText, Required: true, Pattern: `^[A-Za-z0-9_]+$`},
			{Key: "PORT", Label: "端口", Type: FieldNumber, Default: "8080", Min: floatPtr(1), Max: floatPtr(65535)},
			{Key: "MODE", Label: "模式", Type: FieldSelect, Default: "a",
				Options: []FieldOption{{Label: "A", Value: "a"}, {Label: "B", Value: "b"}}},
			{Key: "FLAG", Label: "开关", Type: FieldBool, Default: "false"},
			{Key: "SECRET", Label: "口令", Type: FieldPassword},
			{Key: "NOTE", Label: "备注", Type: FieldTextarea},
		},
		Install: "# demo\nset -e\n",
	}
}

func TestValidateParams_OK(t *testing.T) {
	app := testApp()
	got, err := ValidateParams(app, map[string]string{
		"NAME": "abc", "PORT": "9090", "MODE": "b", "FLAG": "true", "SECRET": "p@ss", "NOTE": "hi",
	})
	if err != nil {
		t.Fatalf("合法参数不应报错: %v", err)
	}
	if got["NAME"] != "abc" || got["PORT"] != "9090" || got["MODE"] != "b" || got["FLAG"] != "true" {
		t.Errorf("归一化结果不符: %+v", got)
	}
	if got["SECRET"] != "p@ss" || got["NOTE"] != "hi" {
		t.Errorf("password/textarea 值应原样保留: %+v", got)
	}
}

func TestValidateParams_Defaults(t *testing.T) {
	app := testApp()
	// 只给必填项，其余应回落默认值；SECRET/NOTE 无默认 → 留空（不出现）
	got, err := ValidateParams(app, map[string]string{"NAME": "abc"})
	if err != nil {
		t.Fatalf("回落默认值不应报错: %v", err)
	}
	if got["PORT"] != "8080" || got["MODE"] != "a" || got["FLAG"] != "false" {
		t.Errorf("默认值回落不符: %+v", got)
	}
	if _, ok := got["SECRET"]; ok {
		t.Errorf("无默认的非必填字段不应写入结果: %+v", got)
	}
}

func TestValidateParams_Errors(t *testing.T) {
	app := testApp()
	cases := []struct {
		name string
		in   map[string]string
		want string // 期望错误文案包含的子串
	}{
		{"必填缺失", map[string]string{}, "不能为空"},
		{"数字非法", map[string]string{"NAME": "x", "PORT": "abc"}, "必须是数字"},
		{"数字超范围", map[string]string{"NAME": "x", "PORT": "70000"}, "不能大于"},
		{"数字低于下界", map[string]string{"NAME": "x", "PORT": "0"}, "不能小于"},
		{"候选非法", map[string]string{"NAME": "x", "MODE": "z"}, "不在候选项中"},
		{"布尔非法", map[string]string{"NAME": "x", "FLAG": "maybe"}, "必须是布尔值"},
		{"正则不匹配", map[string]string{"NAME": "有中文"}, "格式不正确"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateParams(app, tc.in)
			if err == nil {
				t.Fatalf("期望报错但通过")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误文案 %q 未包含 %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateParams_UnknownKeyIgnored(t *testing.T) {
	app := testApp()
	got, err := ValidateParams(app, map[string]string{
		"NAME": "abc", "EVIL; rm -rf /": "x", "PATH": "/etc",
	})
	if err != nil {
		t.Fatalf("未知键应被忽略而非报错: %v", err)
	}
	if _, ok := got["EVIL; rm -rf /"]; ok {
		t.Error("未声明的键不得进入结果（防任意 shell 变量名注入）")
	}
	if _, ok := got["PATH"]; ok {
		t.Error("未声明的键不得进入结果")
	}
}

func TestValidateParams_BoolNormalize(t *testing.T) {
	app := App{Fields: []Field{{Key: "F", Label: "开关", Type: FieldBool}}}
	for in, want := range map[string]string{"1": "true", "YES": "true", "off": "false", "0": "false"} {
		got, err := ValidateParams(app, map[string]string{"F": in})
		if err != nil {
			t.Fatalf("布尔 %q 应合法: %v", in, err)
		}
		if got["F"] != want {
			t.Errorf("布尔 %q 归一为 %q，期望 %q", in, got["F"], want)
		}
	}
}

func TestRenderScript(t *testing.T) {
	app := testApp()
	// 无参数：原样返回脚本
	if got := app.RenderScript(nil); got != app.Install {
		t.Errorf("无参数应原样返回 Install，得到: %q", got)
	}
	// 有参数：前置赋值，单引号包裹，且不破坏脚本正文
	out := app.RenderScript(map[string]string{"NAME": "a'b", "PORT": "80"})
	if !strings.Contains(out, "NAME='a'\\''b'\n") {
		t.Errorf("单引号未按 POSIX 规则转义: %q", out)
	}
	if !strings.Contains(out, "PORT='80'\n") {
		t.Errorf("缺少 PORT 赋值: %q", out)
	}
	if !strings.HasSuffix(out, app.Install) {
		t.Errorf("脚本正文应原样保留在尾部: %q", out)
	}
	// 只输出 schema 声明的键（即便传入多余键）
	out2 := app.RenderScript(map[string]string{"NAME": "x", "EXTRA": "y"})
	if strings.Contains(out2, "EXTRA") {
		t.Errorf("未声明的键不得渲染进脚本: %q", out2)
	}
}

// TestCatalogFieldsIntegrity 目录中所有参数 schema 的静态自洽性 + 「Key 必须被脚本实际读取」。
func TestCatalogFieldsIntegrity(t *testing.T) {
	validTypes := map[FieldType]bool{
		FieldText: true, FieldNumber: true, FieldPassword: true,
		FieldSelect: true, FieldBool: true, FieldTextarea: true,
	}
	for _, app := range All() {
		seen := make(map[string]bool, len(app.Fields))
		for _, f := range app.Fields {
			if f.Key == "" || f.Label == "" {
				t.Errorf("应用 %s 存在缺少 Key/Label 的参数", app.ID)
			}
			if seen[f.Key] {
				t.Errorf("应用 %s 的参数 Key 重复: %s", app.ID, f.Key)
			}
			seen[f.Key] = true
			if !validTypes[f.Type] {
				t.Errorf("应用 %s 的参数 %s 类型非法: %s", app.ID, f.Key, f.Type)
			}
			if f.Type == FieldSelect && len(f.Options) == 0 {
				t.Errorf("应用 %s 的 select 参数 %s 缺少候选项", app.ID, f.Key)
			}
			if f.Pattern != "" {
				if _, err := regexp.Compile(f.Pattern); err != nil {
					t.Errorf("应用 %s 的参数 %s 正则无法编译: %v", app.ID, f.Key, err)
				}
			}
			// 契约核心：Key 必须与脚本读取的变量名一致，否则注入的参数是死值
			if !strings.Contains(app.Install, f.Key) {
				t.Errorf("应用 %s 的安装脚本未引用参数 %s（Key 与脚本变量名不一致）", app.ID, f.Key)
			}
			// 默认值必须能通过自身校验（否则「不传参」场景会失败）
			if f.Default != "" {
				if _, err := ValidateParams(app, map[string]string{f.Key: f.Default}); err != nil {
					t.Errorf("应用 %s 的参数 %s 默认值 %q 无法通过校验: %v", app.ID, f.Key, f.Default, err)
				}
			}
		}
	}
}
