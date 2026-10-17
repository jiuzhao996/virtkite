// survey.go：Playbook「执行参数定义」（Survey Var，参考 Semaphore SurveyVar 设计）——
// 类型/常量、头部注释块解析与归一化。
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
//
// 定义校验见 survey_validate.go，执行侧答案注入见 survey_apply.go。
package ansible

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
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
