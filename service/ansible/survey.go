// survey.go：Playbook「执行参数定义」（Survey Var，参考 Semaphore SurveyVar 设计）。
//
// 载体选型：定义写在 playbook 头部的注释块里，紧跟在 # vmops-playbook: 行之后，
// 形如：
//
//	# vmops-playbook: name=x | desc=y | targets=linux
//	# vmops-survey:
//	#   - name: port
//	#     type: int
//	#     default_value: "8080"
//	#     required: true
//	#   - name: env
//	#     type: select
//	#     values:
//	#       - {name: 生产, value: prod}
//	#       - {name: 测试, value: test}
//	---
//
// 为什么放头部而不是独立的变量定义文件：定义与 playbook 同生共死——种子的复制、
// 目录/单文件两种形态、重命名都不必再维护第二处路径；且注释块天然被 ansible 忽略，
// 不影响 --syntax-check 与执行。用 YAML（而非把类型塞进 vars= 单行语法）是因为
// select/enum 的候选值是数组，塞进单行要自造一套分隔符转义，误解析风险远高于收益。
//
// 向后兼容：没有 survey 块时，回落到旧的 `vars=a,b,c` 逗号清单，每个按 str 处理
// （见 ParseSurvey 调用方 handler.ListPlaybooks）。旧 playbook 一行不改照常可用。
package ansible

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/jiuzhao/vmops/service/secretbox"
)

// Survey 变量类型集合（与 Semaphore 对齐）。
const (
	SurveyTypeStr    = "str"    // 单行文本
	SurveyTypeInt    = "int"    // 整数
	SurveyTypeEnum   = "enum"   // 枚举下拉（候选必须是标量）
	SurveyTypeSelect = "select" // 下拉选择（候选可为 {name,value}）
	SurveyTypeSecret = "secret" // 密文：不进 extra-vars，落库加密，经环境变量注入
	SurveyTypeText   = "text"   // 多行文本
)

// Survey 变量的注入通道。
const (
	SurveyTargetVars = "vars" // 经 --extra-vars @file 注入（默认）
	SurveyTargetEnv  = "env"  // 经环境变量注入（secret 类型强制走这里）
)

// surveyEnvPrefix 环境注入的固定前缀。绝不裸用变量名当环境键——否则一个名叫 PATH /
// LD_PRELOAD 的 survey 变量就能改写 ansible 子进程的运行环境（劫持动态库 = 宿主机 RCE）。
// 加前缀后，用户可控的名字无法命中任何有特殊语义的环境变量。
const surveyEnvPrefix = "VMOPS_"

// surveyNameRe 变量名白名单：必须是合法标识符（既是 ansible 变量名，也是 YAML 键）。
var surveyNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// SurveyValue 一个候选值（下拉选项）：Name 是展示名，Value 是实际注入值。
type SurveyValue struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value" yaml:"value"`
}

// SurveyVar 一个执行参数定义（对齐 Semaphore SurveyVar 字段）。
type SurveyVar struct {
	Name         string        `json:"name" yaml:"name"`
	Title        string        `json:"title,omitempty" yaml:"title,omitempty"`
	Type         string        `json:"type,omitempty" yaml:"type,omitempty"`
	Required     bool          `json:"required,omitempty" yaml:"required,omitempty"`
	Target       string        `json:"target,omitempty" yaml:"target,omitempty"`
	Description  string        `json:"description,omitempty" yaml:"description,omitempty"`
	DefaultValue string        `json:"default_value,omitempty" yaml:"default_value,omitempty"`
	Values       []SurveyValue `json:"values,omitempty" yaml:"values,omitempty"`
}

// rawSurveyVar 解码中间态：Values / DefaultValue 用 interface{} 接，随后归一化——
// 这样候选值既能写标量（enum：`- prod`）也能写 {name,value} 映射（select），
// 不必依赖 yaml 库的自定义 UnmarshalYAML 接口（跨库行为不稳定）。
type rawSurveyVar struct {
	Name         string        `yaml:"name"`
	Title        string        `yaml:"title"`
	Type         string        `yaml:"type"`
	Required     bool          `yaml:"required"`
	Target       string        `yaml:"target"`
	Description  string        `yaml:"description"`
	DefaultValue interface{}   `yaml:"default_value"`
	Values       []interface{} `yaml:"values"`
}

// surveyMarker 头部 survey 块起始标记（去掉前导 # 后的内容）。
const surveyMarker = "vmops-survey:"

// ParseSurvey 从 playbook 原文解析 survey 块。
// 返回 (定义, 是否声明了 survey, 错误)：未声明时 present=false，调用方回落旧 vars= 清单。
// 只扫「首个非注释行之前」的注释块（即 --- 之前），正文里的 # 注释不会被误当定义。
func ParseSurvey(raw []byte) ([]SurveyVar, bool, error) {
	var block []string
	collecting := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if collecting {
				block = append(block, "")
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			break // 注释块结束（遇到 --- 或正文）
		}
		// 去掉 # 与其后一个空格，保留 YAML 缩进
		content := strings.TrimPrefix(trimmed, "#")
		content = strings.TrimPrefix(content, " ")
		if !collecting {
			if strings.TrimSpace(content) == surveyMarker {
				collecting = true
			}
			continue
		}
		block = append(block, content)
	}
	if !collecting {
		return nil, false, nil
	}
	if strings.TrimSpace(strings.Join(block, "\n")) == "" {
		return nil, true, nil // 声明了 survey 但块为空 = 无参数
	}
	var raws []rawSurveyVar
	if err := yaml.Unmarshal([]byte(strings.Join(block, "\n")), &raws); err != nil {
		return nil, true, fmt.Errorf("survey 定义解析失败: %w", err)
	}
	out := make([]SurveyVar, 0, len(raws))
	for _, r := range raws {
		out = append(out, normalizeSurveyVar(r))
	}
	return out, true, nil
}

// normalizeSurveyVar 把解码中间态归一化为公开结构。
func normalizeSurveyVar(r rawSurveyVar) SurveyVar {
	v := SurveyVar{
		Name:        strings.TrimSpace(r.Name),
		Title:       strings.TrimSpace(r.Title),
		Type:        strings.TrimSpace(strings.ToLower(r.Type)),
		Required:    r.Required,
		Target:      strings.TrimSpace(strings.ToLower(r.Target)),
		Description: strings.TrimSpace(r.Description),
	}
	if r.DefaultValue != nil {
		v.DefaultValue = scalarString(r.DefaultValue)
	}
	for _, raw := range r.Values {
		v.Values = append(v.Values, normalizeSurveyValue(raw))
	}
	return v
}

// normalizeSurveyValue 候选值归一化：标量 → {Name=Value=标量}；映射 → 取 name/value。
func normalizeSurveyValue(raw interface{}) SurveyValue {
	switch t := raw.(type) {
	case map[string]interface{}:
		name := scalarString(t["name"])
		val := name
		if vv, ok := t["value"]; ok && vv != nil {
			val = scalarString(vv)
		}
		return SurveyValue{Name: name, Value: val}
	case map[interface{}]interface{}:
		name := scalarString(t["name"])
		val := name
		if vv, ok := t["value"]; ok && vv != nil {
			val = scalarString(vv)
		}
		return SurveyValue{Name: name, Value: val}
	default:
		s := scalarString(raw)
		return SurveyValue{Name: s, Value: s}
	}
}

// scalarString 把 YAML 标量转成字符串（int/bool 也照转，如 8080 → "8080"）。
func scalarString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		// YAML 整数会解成 float64，去尾零
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// DisplayName 变量的展示名（Title 优先，回落 Name）。
func (v SurveyVar) DisplayName() string {
	if strings.TrimSpace(v.Title) != "" {
		return v.Title
	}
	return v.Name
}

// EffectiveTarget 变量的实际注入通道：secret 强制 env；其余按声明（默认 vars）。
func (v SurveyVar) EffectiveTarget() string {
	if v.Type == SurveyTypeSecret {
		return SurveyTargetEnv
	}
	if v.Target == SurveyTargetEnv {
		return SurveyTargetEnv
	}
	return SurveyTargetVars
}

// SurveyEnvKey 环境注入的键名（前缀 + 变量名）。
func SurveyEnvKey(name string) string { return surveyEnvPrefix + name }

// ValidateSurvey 强校验 survey 定义（保存 playbook 前调用）。失败返回中文原因。
// 覆盖：名字合法/唯一、类型在集合内、target 合法、候选值约束（select 需数组、
// enum 需标量）、默认值语义（int 可解析、enum/select 在候选集合内）。
func ValidateSurvey(vars []SurveyVar) error {
	seen := map[string]bool{}
	for i := range vars {
		v := &vars[i]
		if !surveyNameRe.MatchString(v.Name) {
			return fmt.Errorf("第 %d 个变量名 %q 非法（须字母/下划线开头，仅含字母数字下划线）", i+1, v.Name)
		}
		if seen[v.Name] {
			return fmt.Errorf("变量名重复：%s", v.Name)
		}
		seen[v.Name] = true
		if v.Type == "" {
			v.Type = SurveyTypeStr
		}
		switch v.Type {
		case SurveyTypeStr, SurveyTypeInt, SurveyTypeEnum, SurveyTypeSelect, SurveyTypeSecret, SurveyTypeText:
		default:
			return fmt.Errorf("变量 %s 的类型 %q 不支持（可选 str/int/enum/select/secret/text）", v.Name, v.Type)
		}
		if v.Target == "" {
			v.Target = SurveyTargetVars
		}
		if v.Target != SurveyTargetVars && v.Target != SurveyTargetEnv {
			return fmt.Errorf("变量 %s 的 target %q 非法（可选 vars/env）", v.Name, v.Target)
		}
		switch v.Type {
		case SurveyTypeSelect:
			if len(v.Values) == 0 {
				return fmt.Errorf("select 变量 %s 的候选值（values）必须是非空数组", v.Name)
			}
		case SurveyTypeEnum:
			if len(v.Values) == 0 {
				return fmt.Errorf("enum 变量 %s 的候选值（values）必须是非空数组", v.Name)
			}
			for _, c := range v.Values {
				if c.Name != c.Value {
					return fmt.Errorf("enum 变量 %s 的候选值必须是标量，不能是 name/value 对象：%s", v.Name, c.Name)
				}
			}
		default:
			if len(v.Values) > 0 {
				return fmt.Errorf("%s 类型变量 %s 不支持候选值（values）", v.Type, v.Name)
			}
		}
		if v.DefaultValue != "" {
			switch v.Type {
			case SurveyTypeInt:
				if _, err := strconv.Atoi(strings.TrimSpace(v.DefaultValue)); err != nil {
					return fmt.Errorf("int 变量 %s 的默认值 %q 不是合法整数", v.Name, v.DefaultValue)
				}
			case SurveyTypeEnum, SurveyTypeSelect:
				if !surveyHasCandidate(v, v.DefaultValue) {
					return fmt.Errorf("%s 变量 %s 的默认值 %q 不在候选集合内", v.Type, v.Name, v.DefaultValue)
				}
			}
		}
	}
	return nil
}

// surveyHasCandidate 候选集合是否包含某值。
func surveyHasCandidate(v *SurveyVar, val string) bool {
	for _, c := range v.Values {
		if c.Value == val {
			return true
		}
	}
	return false
}

// ValidateAnswers 校验用户提交的答案是否符合定义（执行前调用）。失败返回中文原因。
// 只校验「已声明」的变量：required 必须非空、int 必须可解析、enum/select 必须在候选集合内。
func ValidateAnswers(vars []SurveyVar, answers map[string]string) error {
	for _, v := range vars {
		val := answers[v.Name]
		if strings.TrimSpace(val) == "" {
			if v.Required {
				return fmt.Errorf("「%s」为必填项", v.DisplayName())
			}
			continue
		}
		switch v.Type {
		case SurveyTypeInt:
			if _, err := strconv.Atoi(strings.TrimSpace(val)); err != nil {
				return fmt.Errorf("「%s」需要整数，收到 %q", v.DisplayName(), val)
			}
		case SurveyTypeEnum, SurveyTypeSelect:
			if !surveyHasCandidate(&v, val) {
				return fmt.Errorf("「%s」的值 %q 不在候选集合内", v.DisplayName(), val)
			}
		}
	}
	return nil
}

// WriteExtraVarsFile 把 survey 答案写成 --extra-vars @file 的 YAML 文件，返回文件路径。
//
// 安全要点：字符串值一律打 !unsafe 前缀——用户填的值里若含 {{ }} / {% %}，不加标记会被
// Jinja 当模板求值（用户可控值 → 模板注入）。!unsafe 让 ansible 原样使用该字符串。
// int 类型写裸数字（保留数值语义，加引号会变成字符串）。
// 键名统一走 strconv.Quote 引号化：即便调用方传进含换行/冒号的键名，也不会破坏 YAML
// 文档结构（引号内的键是安全标量），因此不必对键名做正则白名单。
func WriteExtraVarsFile(dir string, vars []SurveyVar, answers map[string]string) (string, error) {
	typeByName := map[string]string{}
	for _, v := range vars {
		typeByName[v.Name] = v.Type
	}
	// 稳定顺序：先按 survey 声明顺序，再补未声明键（字典序），保证输出可复现
	order := make([]string, 0, len(answers))
	declared := map[string]bool{}
	for _, v := range vars {
		if _, ok := answers[v.Name]; ok {
			order = append(order, v.Name)
			declared[v.Name] = true
		}
	}
	rest := make([]string, 0)
	for k := range answers {
		if !declared[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)

	var b strings.Builder
	b.WriteString("# 由 vmops 生成（survey 答案，--extra-vars @file）；字符串打 !unsafe 防 Jinja 注入\n")
	for _, name := range order {
		val := answers[name]
		key := strconv.Quote(name) // 引号化键名：任意键都不会破坏文档结构
		if typeByName[name] == SurveyTypeInt {
			n, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return "", fmt.Errorf("参数 %s 需要整数，收到 %q", name, val)
			}
			fmt.Fprintf(&b, "%s: %d\n", key, n)
			continue
		}
		// strconv.Quote 输出的双引号转义（\n \t \" \\ \xNN \uXXXX）全部是 YAML 双引号标量合法转义
		fmt.Fprintf(&b, "%s: !unsafe %s\n", key, strconv.Quote(val))
	}
	path := filepath.Join(dir, "survey-vars.yml")
	// 0600：答案可能含敏感内容（即便非 secret 类型也不该给同组用户可读），随 run 目录删除
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("写 survey 参数文件失败: %w", err)
	}
	return path, nil
}

// SurveyByName 建名字到定义的索引（执行侧按名字取类型用）。
func SurveyByName(vars []SurveyVar) map[string]SurveyVar {
	m := make(map[string]SurveyVar, len(vars))
	for _, v := range vars {
		m[v.Name] = v
	}
	return m
}

// ApplySurveyAnswers 从 ansible_run 的 payload 装配 survey 注入，返回
// (extra-vars 文件路径, 环境变量表, 错误)。由执行器在跑 playbook 前调用一次。
//
// 分流规则（安全要点）：
//   - 非 secret 答案 → 写成 YAML 文件走 --extra-vars @file（字符串打 !unsafe 防 Jinja 注入），
//     命令行只出现文件路径，用户填的值永不进 argv。
//   - secret 答案 → 明文不进 extra-vars；密文（handler 侧经 secretbox 加密）随 payload
//     落库，此处用主密钥解密后经环境变量注入（键名带 VMOPS_ 前缀，见 SurveyEnvKey）。
//   - target=env 的普通变量同样走环境变量（对齐 Semaphore 的 target 语义）。
//
// 定义从 pbPath（playbook 主文件）头部重新解析——payload 是持久化数据，不无条件信任
// 提交点的校验。无 survey 定义时（旧 vars= 语义），payload.extra_vars 全部按字符串落盘。
func ApplySurveyAnswers(pbPath, runDir string, payload map[string]interface{}, masterSecret string) (string, map[string]string, error) {
	var surveyVars []SurveyVar
	if strings.TrimSpace(pbPath) != "" {
		if raw, err := os.ReadFile(pbPath); err == nil {
			if sv, present, perr := ParseSurvey(raw); perr == nil && present {
				surveyVars = sv
			}
		}
	}
	// 非 secret 答案（payload.extra_vars）
	answers := map[string]string{}
	if raw, ok := payload["extra_vars"].(map[string]interface{}); ok {
		for k, v := range raw {
			answers[k] = fmt.Sprintf("%v", v)
		}
	}
	// secret 答案解密（payload.secret_vars 为 {name: {enc, salt}} 密文）
	secrets := map[string]string{}
	if raw, ok := payload["secret_vars"].(map[string]interface{}); ok && len(raw) > 0 {
		if masterSecret == "" {
			return "", nil, errors.New("服务端未配置凭据主密钥，无法解密执行参数")
		}
		for name, v := range raw {
			m, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			enc, _ := m["enc"].(string)
			salt, _ := m["salt"].(string)
			if enc == "" || salt == "" {
				continue
			}
			plain, derr := secretbox.OpenWithMaster(masterSecret, salt, enc)
			if derr != nil {
				return "", nil, fmt.Errorf("解密执行参数 %s 失败: %w", name, derr)
			}
			secrets[name] = string(plain)
		}
	}
	// 有定义才做答案强校验（无定义 = 旧路径，不额外约束）
	if len(surveyVars) > 0 {
		all := make(map[string]string, len(answers)+len(secrets))
		for k, v := range answers {
			all[k] = v
		}
		for k, v := range secrets {
			all[k] = v
		}
		if verr := ValidateAnswers(surveyVars, all); verr != nil {
			return "", nil, verr
		}
	}
	byName := SurveyByName(surveyVars)
	fileAnswers := map[string]string{}
	envVars := map[string]string{}
	for name, val := range answers {
		if v, ok := byName[name]; ok && v.EffectiveTarget() == SurveyTargetEnv {
			envVars[SurveyEnvKey(name)] = val
			continue
		}
		fileAnswers[name] = val
	}
	for name, val := range secrets {
		envVars[SurveyEnvKey(name)] = val
	}
	extraFile := ""
	if len(fileAnswers) > 0 {
		p, werr := WriteExtraVarsFile(runDir, surveyVars, fileAnswers)
		if werr != nil {
			return "", nil, werr
		}
		extraFile = p
	}
	return extraFile, envVars, nil
}
