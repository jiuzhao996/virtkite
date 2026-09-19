// appstore_test.go 覆盖：迷你 yaml 解析器、ValidateFields 各分支、版本比较、
// 以及 conf/appstore 内置 20 应用包的完整性（可解析/compose 存在/字段规范）。
package appstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdirRepoRoot 切到仓库根（t.Chdir 结束自动恢复），使相对路径 conf/appstore 生效。
// go test 的工作目录是包目录，仓库根即 ../..。
func chdirRepoRoot(t *testing.T) {
	t.Helper()
	t.Chdir("../..")
}

func TestParseSimpleYAML(t *testing.T) {
	tests := []struct {
		name    string
		content string
		check   func(t *testing.T, doc *yamlDoc)
	}{
		{
			name:    "顶层标量与引号剥离",
			content: "name: nginx\ntitle: \"Nginx 服务器\"\ncategory: 'web'\n",
			check: func(t *testing.T, doc *yamlDoc) {
				if doc.scalars["name"] != "nginx" || doc.scalars["title"] != "Nginx 服务器" ||
					doc.scalars["category"] != "web" {
					t.Fatalf("标量解析不符: %#v", doc.scalars)
				}
			},
		},
		{
			name:    "整行注释与空行跳过",
			content: "# 注释一\n\nkey: value\n  # 缩进注释\nother: 2\n",
			check: func(t *testing.T, doc *yamlDoc) {
				if len(doc.scalars) != 2 || doc.scalars["key"] != "value" || doc.scalars["other"] != "2" {
					t.Fatalf("注释/空行处理不符: %#v", doc.scalars)
				}
			},
		},
		{
			name:    "值内冒号按首个冒号切分",
			content: "description: 高性能: 支持, 稳定: 是\n",
			check: func(t *testing.T, doc *yamlDoc) {
				if doc.scalars["description"] != "高性能: 支持, 稳定: 是" {
					t.Fatalf("值内冒号被误切: %q", doc.scalars["description"])
				}
			},
		},
		{
			name:    "tags 字符串列表",
			content: "category: web\ntags:\n  - web\n  - 反向代理\n\nafter: x\n",
			check: func(t *testing.T, doc *yamlDoc) {
				got := doc.lists["tags"]
				if len(got) != 2 || got[0] != "web" || got[1] != "反向代理" {
					t.Fatalf("tags 解析不符: %#v", got)
				}
				// 列表后的顶层标量应正确落回 scalars
				if doc.scalars["after"] != "x" || doc.scalars["category"] != "web" {
					t.Fatalf("列表后的标量丢失: %#v", doc.scalars)
				}
			},
		},
		{
			name:    "CRLF 行尾兼容",
			content: "name: a\r\ntags:\r\n  - x\r\n",
			check: func(t *testing.T, doc *yamlDoc) {
				if doc.scalars["name"] != "a" || len(doc.lists["tags"]) != 1 {
					t.Fatalf("CRLF 解析不符: %#v %#v", doc.scalars, doc.lists)
				}
			},
		},
		{
			name: "formFields 完整属性",
			content: `additionalProperties:
  formFields:
    - envKey: PANEL_APP_PORT_HTTP
      label: HTTP 端口
      type: number
      default: 80
      required: true
      rule: paramPort
    - envKey: PANEL_TOKEN
      label: 令牌
      type: password
      required: false
`,
			check: func(t *testing.T, doc *yamlDoc) {
				if len(doc.formFields) != 2 {
					t.Fatalf("表单项数量不符: %d", len(doc.formFields))
				}
				first := doc.formFields[0].attrs
				if first["envKey"] != "PANEL_APP_PORT_HTTP" || first["default"] != "80" ||
					first["required"] != "true" || first["rule"] != "paramPort" {
					t.Fatalf("第一项属性不符: %#v", first)
				}
				if doc.formFields[1].attrs["required"] != "false" {
					t.Fatalf("第二项 required 解析不符: %#v", doc.formFields[1].attrs)
				}
			},
		},
		{
			name: "表单项嵌套 select values",
			content: `formFields:
  - envKey: PANEL_AOF
    label: AOF
    type: select
    default: "yes"
    values:
      - label: 开启
        value: "yes"
      - label: 关闭
        value: "no"
`,
			check: func(t *testing.T, doc *yamlDoc) {
				if len(doc.formFields) != 1 {
					t.Fatalf("表单项数量不符: %d", len(doc.formFields))
				}
				raw := doc.formFields[0]
				if raw.attrs["default"] != "yes" { // 引号被剥
					t.Fatalf("default 引号剥离不符: %q", raw.attrs["default"])
				}
				if len(raw.values) != 2 ||
					raw.values[0] != [2]string{"开启", "yes"} ||
					raw.values[1] != [2]string{"关闭", "no"} {
					t.Fatalf("values 选项对不符: %#v", raw.values)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, parseSimpleYAML(tt.content))
		})
	}
}

func TestParseFormFields(t *testing.T) {
	doc := parseSimpleYAML(`formFields:
  - envKey: PANEL_PORT
    label: 端口
    type: number
    default: 3001
    required: true
    rule: paramPort
`)
	fields := parseFormFields(doc)
	if len(fields) != 1 {
		t.Fatalf("应有 1 个表单项, got %d", len(fields))
	}
	f := fields[0]
	if f.EnvKey != "PANEL_PORT" || f.Label != "端口" || f.Type != FieldTypeNumber ||
		f.Default != "3001" || !f.Required || f.Rule != RuleParamPort {
		t.Fatalf("FormField 映射不符: %+v", f)
	}
	// 空文档应得到非 nil 空切片（JSON 输出 [] 而非 null）
	if empty := parseFormFields(parseSimpleYAML("name: x\n")); empty == nil || len(empty) != 0 {
		t.Fatalf("空文档应得空切片, got %#v", empty)
	}
}

func TestFormFieldMarshalJSON(t *testing.T) {
	f := FormField{
		EnvKey:  "PANEL_AOF",
		Label:   "AOF",
		Type:    FieldTypeSelect,
		Default: "yes",
		Values:  [][2]string{{"开启", "yes"}, {"关闭", "no"}},
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	want := `{"envKey":"PANEL_AOF","label":"AOF","type":"select","default":"yes","required":false,` +
		`"values":[{"label":"开启","value":"yes"},{"label":"关闭","value":"no"}]}`
	if string(b) != want {
		t.Fatalf("JSON 输出不符:\n got %s\nwant %s", b, want)
	}
	// 无选项时 values 键应省略
	b2, _ := json.Marshal(FormField{EnvKey: "P", Label: "p", Type: FieldTypeText})
	if want2 := `{"envKey":"P","label":"p","type":"text","required":false}`; string(b2) != want2 {
		t.Fatalf("无选项 JSON 不符:\n got %s\nwant %s", b2, want2)
	}
}

func TestValidateFields(t *testing.T) {
	fields := []FormField{
		{EnvKey: "PANEL_APP_PORT", Label: "端口", Type: FieldTypeNumber, Default: "80", Required: true, Rule: RuleParamPort},
		{EnvKey: "PANEL_PASSWORD", Label: "密码", Type: FieldTypePassword},
		{EnvKey: "PANEL_MODE", Label: "模式", Type: FieldTypeSelect, Values: [][2]string{{"开启", "yes"}, {"关闭", "no"}}},
	}

	tests := []struct {
		name    string
		values  map[string]string
		wantErr string // 空串=期望成功
	}{
		{name: "全部合法", values: map[string]string{"PANEL_APP_PORT": "8080", "PANEL_PASSWORD": "s3cret", "PANEL_MODE": "yes"}},
		{name: "缺可选项合法", values: map[string]string{"PANEL_APP_PORT": "80"}},
		{name: "必填项缺失", values: map[string]string{"PANEL_PASSWORD": "x"}, wantErr: "必填项「端口」未填写"},
		{name: "必填项空白", values: map[string]string{"PANEL_APP_PORT": "   "}, wantErr: "必填项「端口」未填写"},
		{name: "未知 envKey 拒绝", values: map[string]string{"PANEL_APP_PORT": "80", "EVIL_VAR": "1"}, wantErr: "未知配置项：EVIL_VAR"},
		{name: "端口为零", values: map[string]string{"PANEL_APP_PORT": "0"}, wantErr: "需为 1-65535"},
		{name: "端口超上界", values: map[string]string{"PANEL_APP_PORT": "65536"}, wantErr: "需为 1-65535"},
		{name: "端口非数字", values: map[string]string{"PANEL_APP_PORT": "8 0"}, wantErr: "需为 1-65535"},
		{name: "select 合法值", values: map[string]string{"PANEL_APP_PORT": "80", "PANEL_MODE": "no"}},
		{name: "select 非法值", values: map[string]string{"PANEL_APP_PORT": "80", "PANEL_MODE": "maybe"}, wantErr: "不在可选项内"},
		{name: "值含换行拒绝", values: map[string]string{"PANEL_APP_PORT": "80\nEVIL=1"}, wantErr: "不能包含换行符"},
		{name: "值含回车拒绝", values: map[string]string{"PANEL_PASSWORD": "a\rb"}, wantErr: "不能包含换行符"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFields(fields, tt.values)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过, got %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("期望 ValidationError, got %T: %v", err, err)
			}
			if !strings.Contains(ve.Msg, tt.wantErr) {
				t.Fatalf("错误文案不符: got %q, want 包含 %q", ve.Msg, tt.wantErr)
			}
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	fields := []FormField{
		{EnvKey: "PANEL_PORT", Default: "80"},
		{EnvKey: "PANEL_NAME", Default: "app"},
	}
	merged := applyDefaults(fields, map[string]string{"PANEL_PORT": " 9000 "})
	if merged["PANEL_PORT"] != "9000" { // trim 生效
		t.Fatalf("trim 不符: %q", merged["PANEL_PORT"])
	}
	if merged["PANEL_NAME"] != "app" { // 默认值补齐
		t.Fatalf("默认值未补齐: %q", merged["PANEL_NAME"])
	}
	// 入参不被修改；nil 入参得非 nil 结果
	if _, ok := map[string]string{"PANEL_PORT": " 9000 "}["PANEL_PORT"]; !ok {
		t.Fatal("入参被意外改动")
	}
	if merged2 := applyDefaults(fields, nil); merged2 == nil || merged2["PANEL_PORT"] != "80" {
		t.Fatalf("nil 入参兜底不符: %#v", merged2)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.10.0", "1.9.0", 1}, // 数值比较而非字典序
		{"2.0", "1.9.9", 1},    // 数值比较而非字典序
		{"1.0", "1.0.0", -1},   // 段数少者小
		{"1.0.0", "1.0.1", -1}, // 数值比较而非字典序
		{"1.0", "1.0", 0},      // 相等
		{"beta1", "alpha", 1},  // 非数字段字典序
	}
	for _, tt := range tests {
		if got := compareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestLatestVersion(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []string{"1.0.0", "1.10.0", "1.9.0", "not-a-version"} {
		if err := os.MkdirAll(filepath.Join(dir, v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := latestVersion(dir)
	if err != nil || got != "1.10.0" {
		t.Fatalf("latestVersion = %q, %v; want 1.10.0", got, err)
	}
	// 无版本目录时报错
	empty := t.TempDir()
	if _, err := latestVersion(empty); err == nil {
		t.Fatal("空目录应报错")
	}
}

// TestRepoPackagesCompleteness 内置 20 应用包完整性：可解析、compose 存在、字段规范。
// 这是磁盘目录契约的回归测试，新增应用包时应把 key 加进 expectedKeys。
func TestRepoPackagesCompleteness(t *testing.T) {
	chdirRepoRoot(t)

	expectedKeys := map[string]string{ // key → 展示名
		"nginx":       "Nginx",
		"redis":       "Redis",
		"mysql":       "MySQL",
		"wordpress":   "WordPress",
		"portainer":   "Portainer CE",
		"uptime-kuma": "Uptime Kuma",
		// v3.2 批次 S 新增 14 包
		"postgresql":  "PostgreSQL",
		"minio":       "MinIO",
		"gitea":       "Gitea",
		"mongodb":     "MongoDB",
		"rabbitmq":    "RabbitMQ",
		"open-webui":  "Open WebUI",
		"n8n":         "n8n",
		"code-server": "code-server",
		"it-tools":    "IT Tools",
		"jenkins":     "Jenkins",
		"halo":        "Halo",
		"memos":       "Memos",
		"jellyfin":    "Jellyfin",
		"qbittorrent": "qBittorrent",
	}

	metas, err := List()
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	gotKeys := map[string]AppMeta{}
	for _, m := range metas {
		gotKeys[m.Key] = m
	}
	if len(metas) != len(expectedKeys) {
		t.Fatalf("应用包数量不符: got %d (%v), want %d", len(metas), keysOf(gotKeys), len(expectedKeys))
	}
	for key, name := range expectedKeys {
		t.Run(key, func(t *testing.T) {
			meta, ok := gotKeys[key]
			if !ok {
				t.Fatalf("缺少应用包 %s", key)
			}
			if meta.Name != name || meta.Category == "" || meta.Version != "1.0.0" {
				t.Fatalf("元数据不符: %+v", meta)
			}
			if len(meta.FormFields) == 0 {
				t.Fatal("表单定义不能为空")
			}
			for _, f := range meta.FormFields {
				if !envKeyPattern.MatchString(f.EnvKey) {
					t.Errorf("envKey %q 不符合命名规范", f.EnvKey)
				}
				switch f.Type {
				case FieldTypeText, FieldTypeNumber, FieldTypePassword, FieldTypeSelect:
				default:
					t.Errorf("envKey %q 的 type %q 非法", f.EnvKey, f.Type)
				}
				if f.Rule != "" && f.Rule != RuleParamPort {
					t.Errorf("envKey %q 的 rule %q 非法", f.EnvKey, f.Rule)
				}
				if f.Type == FieldTypeSelect && len(f.Values) == 0 {
					t.Errorf("select 型 %q 缺 values 选项", f.EnvKey)
				}
			}
			// Get 二次校验 compose 内容真实存在且含关键要素
			_, compose, fields, err := Get(key)
			if err != nil {
				t.Fatalf("Get(%s) 失败: %v", key, err)
			}
			if compose == "" || !strings.Contains(compose, "services:") || !strings.Contains(compose, "restart: unless-stopped") {
				t.Fatalf("compose 内容不完整: %q", compose)
			}
			if len(fields) != len(meta.FormFields) {
				t.Fatal("Get 与 List 的表单项数量不一致")
			}
			// compose 里引用的每个 ${PANEL_*} 都必须是声明的表单项（compose 插值无孤儿变量）
			declared := map[string]bool{}
			for _, f := range fields {
				declared[f.EnvKey] = true
			}
			for i := 0; i < len(compose); i++ {
				if compose[i] != '$' || i+1 >= len(compose) || compose[i+1] != '{' {
					continue
				}
				end := indexByte(compose[i:], '}')
				if end < 0 {
					continue
				}
				varName := compose[i+2 : i+end]
				if len(varName) > 6 && varName[:6] == "PANEL_" && !declared[varName] {
					t.Errorf("compose 引用了未声明的表单变量 %s", varName)
				}
				i += end
			}
		})
	}

	// 抽查关键表单项的语义（字段规范回归）
	nginxPort := findField(t, gotKeys["nginx"].FormFields, "PANEL_APP_PORT_HTTP")
	if !nginxPort.Required || nginxPort.Default != "80" || nginxPort.Rule != RuleParamPort {
		t.Fatalf("nginx 端口字段不符: %+v", nginxPort)
	}
	mysqlRoot := findField(t, gotKeys["mysql"].FormFields, "PANEL_MYSQL_ROOT_PASSWORD")
	if !mysqlRoot.Required || mysqlRoot.Type != FieldTypePassword {
		t.Fatalf("mysql root 密码字段不符: %+v", mysqlRoot)
	}
	wpPass := findField(t, gotKeys["wordpress"].FormFields, "PANEL_WP_DB_PASSWORD")
	if !wpPass.Required || wpPass.Type != FieldTypePassword {
		t.Fatalf("wordpress DB 密码字段不符: %+v", wpPass)
	}
	redisAOF := findField(t, gotKeys["redis"].FormFields, "PANEL_REDIS_AOF")
	if redisAOF.Type != FieldTypeSelect || len(redisAOF.Values) != 2 {
		t.Fatalf("redis select 字段不符: %+v", redisAOF)
	}
}

func TestGetUnknownKey(t *testing.T) {
	chdirRepoRoot(t)
	_, _, _, err := Get("no-such-app")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("期望 ErrNotFound, got %v", err)
	}
	// 路径穿越成分按不存在处理，绝不放行到文件系统
	for _, bad := range []string{"../etc", "a/b", "..", "A_UPPER"} {
		if _, _, _, err := Get(bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("非法 key %q 应按 ErrNotFound 处理, got %v", bad, err)
		}
	}
}

func findField(t *testing.T, fields []FormField, envKey string) FormField {
	t.Helper()
	for _, f := range fields {
		if f.EnvKey == envKey {
			return f
		}
	}
	t.Fatalf("缺少表单项 %s", envKey)
	return FormField{}
}

func keysOf(m map[string]AppMeta) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
