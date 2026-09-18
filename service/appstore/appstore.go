// Package appstore 应用商店 v2：1Panel 式声明式 Docker Compose 应用包机制。
//
// 与 service/apps（bash 脚本版，经 SSH 装进虚拟机）互不相干——本包面向**宿主机**：
// 每个应用是一个磁盘目录 conf/appstore/<key>/<version>/ 三件套
// （应用级 data.yml 元数据 + 版本级 data.yml 表单定义 + docker-compose.yml ${VAR} 占位）。
// 安装 = 拷贝包到 data/apps/<key>/ + 校验/补默认值 + 写 .env + `docker compose up -d`，
// 变量替换完全交给 compose 原生 --env-file 插值，本包不做任何模板渲染。
//
// 部署形态为单机同目录（后端进程工作目录 = 仓库根），conf/appstore 与 data/apps 均为相对路径，
// 刻意不用 go:embed（运维可直接在磁盘上增改应用包，无需重编译）。
//
// yaml 解析刻意不引入 gopkg.in/yaml.v3（它在 go.mod 里只是间接依赖，动 direct 依赖改 go.mod
// 的收益配不上本格式）：data.yml 结构受控（两层级 key: value + formFields/values 列表），
// 由本文件 parseSimpleYAML 行式读取器覆盖，覆盖不到的完整 YAML 语法（锚点/多行字面量/流式写法）
// 一律视为不支持，写包时不要用。
package appstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// 表单项 type 取值（前端按 type 渲染对应输入控件）
const (
	FieldTypeText     = "text"
	FieldTypeNumber   = "number"
	FieldTypePassword = "password"
	FieldTypeSelect   = "select"
)

// 表单项 rule 取值：paramPort = 值必须是 1-65535 的端口号（与 handler/param.go 的 paramID
// 系列的端口校验同口径）
const RuleParamPort = "paramPort"

var (
	// ErrNotFound 应用不存在（key 非法 / conf/appstore 下无此应用）。消息即用户文案。
	ErrNotFound = errors.New("应用不存在")

	// keyPattern 应用目录名：小写字母数字开头，允许小写字母/数字/连字符。
	// 同时是路径安全守卫——key 会拼进文件路径，拒绝 / \ .. 等路径穿越成分。
	keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

	// versionPattern 版本目录名：数字开头，允许数字/字母/点/连字符（1.0.0、2.21.4、1.0.0-beta）；
	// 数字开头避免把杂物目录误当版本
	versionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.-]{0,31}$`)

	// envKeyPattern .env 键名守卫：写入 .env 的每一行是 KEY=VALUE，
	// 键里混入换行/等号会伪造额外变量行，宁严勿宽
	envKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// FormField 安装表单项：envKey 是写入 .env 的变量名（compose 文件里用 ${envKey} 引用），
// 其余字段描述前端如何渲染输入控件。
type FormField struct {
	EnvKey   string      `json:"envKey"`
	Label    string      `json:"label"`
	Type     string      `json:"type"`
	Default  string      `json:"default,omitempty"`
	Required bool        `json:"required"`
	Rule     string      `json:"rule,omitempty"`
	Values   [][2]string `json:"-"` // label/value 对；JSON 输出经 MarshalJSON 转为 [{"label","value"}]
}

// MarshalJSON 定制 values 的输出形状：内部存 [][2]string（紧凑省内存），
// 对外输出 [{"label":"开启","value":"yes"}]，前端 el-option 可直接消费。
func (f FormField) MarshalJSON() ([]byte, error) {
	type plain FormField
	out := struct {
		plain
		Values []map[string]string `json:"values,omitempty"`
	}{plain: plain(f)}
	if len(f.Values) > 0 {
		out.Values = make([]map[string]string, 0, len(f.Values))
		for _, v := range f.Values {
			out.Values = append(out.Values, map[string]string{"label": v[0], "value": v[1]})
		}
	}
	return json.Marshal(out)
}

// AppMeta 应用目录条目（List/Get 响应体）。
type AppMeta struct {
	Key         string      `json:"key"`
	Name        string      `json:"name"`
	Category    string      `json:"category"`
	Description string      `json:"description,omitempty"`
	Tags        []string    `json:"tags"`
	Version     string      `json:"version"` // 最新版本目录名（本包不做多版本共存安装）
	FormFields  []FormField `json:"formFields"`
}

// ValidationError 安装参数校验失败。消息为面向用户的中文文案（不含内部细节），
// handler 捕获后以 400 原样透出。
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// List 扫描 conf/appstore/ 返回全部应用目录（含各应用最新版本与表单定义）。
// 目录不存在视为空目录（返回空数组而非错误，与全仓「列表接口空数据返回 [] 非 null」口径一致）；
// 单个应用包损坏（缺 data.yml/compose）则整体报错——内置包应当损坏即暴露，不做静默缺项。
func List() ([]AppMeta, error) {
	entries, err := os.ReadDir(catalogDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []AppMeta{}, nil
		}
		return nil, fmt.Errorf("读取应用包目录失败: %w", err)
	}
	metas := []AppMeta{}
	for _, e := range entries {
		// 目录名不符合 key 规范的（杂物目录）直接跳过，不让整个目录页挂掉
		if !e.IsDir() || !keyPattern.MatchString(e.Name()) {
			continue
		}
		meta, _, _, err := load(e.Name(), "")
		if err != nil {
			return nil, err
		}
		metas = append(metas, meta)
	}
	return metas, nil
}

// Get 读取单个应用：元数据（含最新版本与表单）、compose 文件原文（预览用）、表单项列表。
// key 非法或应用不存在返回 ErrNotFound。
func Get(key string) (*AppMeta, string, []FormField, error) {
	meta, compose, _, err := load(key, "")
	if err != nil {
		return nil, "", nil, err
	}
	return &meta, compose, meta.FormFields, nil
}

// load 读取应用包。version 为空时取最新版本目录；返回值带版本目录绝对……相对路径（安装时拷贝源）。
func load(key, version string) (AppMeta, string, string, error) {
	if !keyPattern.MatchString(key) {
		// 不回显命名规则细节（避免给攻击者喂路径探测信息），统一按不存在处理
		return AppMeta{}, "", "", fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	appDir := filepath.Join(catalogDir, key)

	content, err := os.ReadFile(filepath.Join(appDir, "data.yml"))
	if err != nil {
		if os.IsNotExist(err) {
			return AppMeta{}, "", "", fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return AppMeta{}, "", "", fmt.Errorf("读取应用包 data.yml 失败: %w", err)
	}
	appDoc := parseSimpleYAML(string(content))

	if version == "" {
		version, err = latestVersion(appDir)
		if err != nil {
			return AppMeta{}, "", "", fmt.Errorf("应用 %s 缺少可用版本: %w", key, err)
		}
	} else if !versionPattern.MatchString(version) {
		return AppMeta{}, "", "", fmt.Errorf("%w: %s（版本号 %q）", ErrNotFound, key, version)
	}
	verDir := filepath.Join(appDir, version)

	verContent, err := os.ReadFile(filepath.Join(verDir, "data.yml"))
	if err != nil {
		return AppMeta{}, "", "", fmt.Errorf("读取版本定义 data.yml 失败: %w", err)
	}
	fields := parseFormFields(parseSimpleYAML(string(verContent)))

	meta := AppMeta{
		Key:         key,
		Name:        appDisplayName(appDoc, key),
		Category:    appDoc.scalars["category"],
		Description: appDoc.scalars["description"],
		Tags:        appDoc.lists["tags"],
		Version:     version,
		FormFields:  fields,
	}
	if meta.Tags == nil {
		meta.Tags = []string{}
	}

	composeBytes, err := os.ReadFile(filepath.Join(verDir, "docker-compose.yml"))
	if err != nil {
		return AppMeta{}, "", "", fmt.Errorf("读取 docker-compose.yml 失败: %w", err)
	}
	return meta, string(composeBytes), verDir, nil
}

// appDisplayName 应用展示名：title 优先，其次 name，最后兜底目录 key。
func appDisplayName(doc *yamlDoc, key string) string {
	if title := doc.scalars["title"]; title != "" {
		return title
	}
	if name := doc.scalars["name"]; name != "" {
		return name
	}
	return key
}

// latestVersion 在应用目录下挑最新版本目录名（按「.」分段数值比较）。
func latestVersion(appDir string) (string, error) {
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return "", fmt.Errorf("读取版本目录失败: %w", err)
	}
	best := ""
	for _, e := range entries {
		if !e.IsDir() || !versionPattern.MatchString(e.Name()) {
			continue
		}
		if best == "" || compareVersions(e.Name(), best) > 0 {
			best = e.Name()
		}
	}
	if best == "" {
		return "", errors.New("没有符合版本命名规范的子目录")
	}
	return best, nil
}

// compareVersions 按「.」分段比较版本号：两段皆为纯数字按数值比，否则按字典序；
// 前缀相同段数不同时，段数多者视为更大（1.0 > 1）。相等返回 0。
func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var sa, sb string
		if i < len(as) {
			sa = as[i]
		}
		if i < len(bs) {
			sb = bs[i]
		}
		if sa == sb {
			continue
		}
		na, ea := strconv.Atoi(sa)
		nb, eb := strconv.Atoi(sb)
		if ea == nil && eb == nil {
			if na < nb {
				return -1
			}
			return 1
		}
		if sa < sb {
			return -1
		}
		return 1
	}
	return 0
}

// ValidateFields 校验用户提交的安装参数（调用方应先经 applyDefaults 补齐默认值）：
//   - 未知 envKey 拒绝（compose 只插值表单声明的变量，多传即配置错误）
//   - 值不允许换行（.env 按行解析，值内换行可伪造额外的 KEY=VALUE 行绕过未知变量拒绝）
//   - rule == paramPort 时必须为 1-65535 整数
//   - select 类型必须命中 values 选项之一
//   - required 项最终值为空报错
func ValidateFields(fields []FormField, values map[string]string) error {
	known := make(map[string]FormField, len(fields))
	for _, f := range fields {
		known[f.EnvKey] = f
	}
	for k, v := range values {
		f, ok := known[k]
		if !ok {
			return &ValidationError{Msg: fmt.Sprintf("未知配置项：%s", k)}
		}
		if strings.ContainsAny(v, "\r\n") {
			return &ValidationError{Msg: fmt.Sprintf("配置项「%s」的值不能包含换行符", f.Label)}
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue // 空值只走必填兜底检查（可选项留空合法）
		}
		if f.Rule == RuleParamPort {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 65535 {
				return &ValidationError{Msg: fmt.Sprintf("配置项「%s」需为 1-65535 之间的端口号", f.Label)}
			}
		}
		if f.Type == FieldTypeSelect && !selectValueAllowed(f.Values, v) {
			return &ValidationError{Msg: fmt.Sprintf("配置项「%s」的值不在可选项内", f.Label)}
		}
	}
	for _, f := range fields {
		if f.Required && strings.TrimSpace(values[f.EnvKey]) == "" {
			return &ValidationError{Msg: fmt.Sprintf("必填项「%s」未填写", f.Label)}
		}
	}
	return nil
}

// selectValueAllowed 判断 v 是否命中选项值
func selectValueAllowed(values [][2]string, v string) bool {
	for _, pair := range values {
		if pair[1] == v {
			return true
		}
	}
	return false
}

// applyDefaults 返回补齐默认值后的参数副本（逐项 trim 空白，不改入参；始终非 nil）
func applyDefaults(fields []FormField, values map[string]string) map[string]string {
	merged := make(map[string]string, len(values)+len(fields))
	for k, v := range values {
		merged[k] = strings.TrimSpace(v)
	}
	for _, f := range fields {
		if merged[f.EnvKey] == "" && f.Default != "" {
			merged[f.EnvKey] = f.Default
		}
	}
	return merged
}

// ---------- 极简 YAML 读取器 ----------

// formFieldRaw 表单项的原始解析形态：attrs 平铺键值 + 嵌套 values 选项对
type formFieldRaw struct {
	attrs  map[string]string
	values [][2]string
}

// yamlDoc data.yml 的解析结果：顶层标量、顶层字符串列表（tags）、formFields 映射列表
type yamlDoc struct {
	scalars    map[string]string
	lists      map[string][]string
	formFields []formFieldRaw
}

// parseSimpleYAML 解析应用包 data.yml，只支持本包约定子集（行式，状态机实现）：
//
//	# 整行注释
//	key: value            顶层标量（值两侧成对引号剥掉，值内冒号安全——按首个冒号切分）
//	tags:                 字符串列表
//	  - web
//	formFields:           映射列表
//	  - envKey: X         表单项以 - envKey: 开始
//	    label: Y          其后缩进行为该项属性
//	    values:           表单项内嵌套选项列表
//	      - label: L
//	        value: V
//
// 不支持：锚点/别名、多行字面量（| >）、行内注释（# 会被当值的一部分）、流式写法 {a: b}。
func parseSimpleYAML(content string) *yamlDoc {
	doc := &yamlDoc{
		scalars: map[string]string{},
		lists:   map[string][]string{},
	}
	const (
		stateTop    = ""       // 顶层
		stateList   = "list"   // 字符串列表（tags）
		stateFields = "fields" // formFields 列表
		stateValues = "values" // 表单项的 values 选项列表
	)
	state := stateTop
	curList := ""              // state==stateList 时的列表键名
	var curField *formFieldRaw // 正在组装的表单项
	var curPair [2]string      // 正在组装的选项对 [label, value]

	flushPair := func() {
		if curPair[0] != "" && curField != nil {
			curField.values = append(curField.values, curPair)
		}
		curPair = [2]string{}
	}
	flushField := func() {
		if curField != nil {
			doc.formFields = append(doc.formFields, *curField)
		}
		curField = nil
	}

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		// 整行注释与空行跳过；行内 # 刻意不处理（描述文案可能含 #）
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := 0
		for indent < len(line) && line[indent] == ' ' {
			indent++
		}
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		isItem := body != trimmed && trimmed != "-"
		if isItem && body == "" {
			continue // 孤立 "-"：空列表项，忽略
		}

		if isItem {
			switch state {
			case stateList:
				doc.lists[curList] = append(doc.lists[curList], body)
			case stateValues:
				k, v, ok := splitKV(body)
				switch {
				case ok && k == "envKey":
					// values 列表结束、下一个表单项开始
					flushPair()
					flushField()
					curField = &formFieldRaw{attrs: map[string]string{"envKey": v}}
					state = stateFields
				case ok && k == "label":
					flushPair()
					curPair[0] = v
				case ok && k == "value":
					curPair[1] = v
					flushPair()
				}
			case stateFields:
				if k, v, ok := splitKV(body); ok && k == "envKey" {
					flushPair()
					flushField()
					curField = &formFieldRaw{attrs: map[string]string{"envKey": v}}
				} else if ok && curField != nil {
					// 表单项属性换行紧排（- envKey: x 换行后 label: y 带 - 的容错）
					curField.attrs[k] = v
				}
			}
			continue
		}

		k, v, ok := splitKV(trimmed)
		if !ok {
			continue // 无冒号的杂行，忽略
		}
		if v != "" {
			switch {
			case state == stateValues && k == "value":
				// 选项 value 独占一行的写法
				curPair[1] = v
				flushPair()
			case state == stateFields && indent > 0 && curField != nil:
				curField.attrs[k] = v
			default:
				// 顶层标量（同时结束任何进行中的列表上下文）
				state = stateTop
				doc.scalars[k] = v
			}
			continue
		}
		// 段落开始（key: 后无值）
		switch k {
		case "tags":
			state = stateList
			curList = "tags"
		case "formFields":
			flushPair()
			flushField()
			state = stateFields
		case "values":
			if state == stateFields {
				state = stateValues
			}
		case "additionalProperties":
			// 纯包装层：不改状态，其下的 formFields 由上面的 case 接管
		default:
			flushPair()
			flushField()
			state = stateTop
		}
	}
	flushPair()
	flushField()
	return doc
}

// splitKV 按首个冒号切分 key: value，值两侧成对引号剥掉（值内冒号安全）。
// 无冒号或空 key 时 ok=false。
func splitKV(s string) (key, value string, ok bool) {
	i := strings.Index(s, ":")
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:i])
	value = unquote(strings.TrimSpace(s[i+1:]))
	return key, value, true
}

// unquote 剥掉两侧成对的单/双引号
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// parseFormFields 把解析出的原始表单项转成对外的 FormField 切片（始终非 nil，JSON 输出为 []）
func parseFormFields(doc *yamlDoc) []FormField {
	fields := make([]FormField, 0, len(doc.formFields))
	for _, raw := range doc.formFields {
		fields = append(fields, FormField{
			EnvKey:   raw.attrs["envKey"],
			Label:    raw.attrs["label"],
			Type:     raw.attrs["type"],
			Default:  raw.attrs["default"],
			Required: raw.attrs["required"] == "true",
			Rule:     raw.attrs["rule"],
			Values:   raw.values,
		})
	}
	return fields
}
