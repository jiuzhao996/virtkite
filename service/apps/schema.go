package apps

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jiuzhao/vmops/service/vmssh"
)

// 本文件实现「安装参数表单」的数据驱动契约：后端为每个应用声明参数 schema，
// 前端按 Type 通用渲染表单，提交时服务端按同一 schema 校验（前端校验可绕过，服务端是唯一可信关卡）。
//
// 参数如何到达脚本（关键约定，务必与 app_install executor 保持一致）：
//   - Field.Key 必须与安装脚本实际读取的 shell 变量名逐字一致（如 WP_PORT）；
//   - 校验归一后的值由 App.RenderScript 以 `KEY='value'` 前置到脚本顶部（单引号包裹，防注入）；
//   - 脚本自身用 `: "${KEY:=默认}"` 兜底，因此未声明 schema 的应用、以及 payload 里没有参数
//     的历史任务，行为与改造前完全一致（向后兼容）。
//
// 安全：password 类型字段的值绝不明文落库——handler 在 HTTP 边界就地加密，
// payload 只存密文与盐（见 handler/apps.go 与 service/tasks/app_tasks.go 的解密口径）。

// FieldType 参数控件类型，前端按此通用渲染。
type FieldType string

const (
	FieldText     FieldType = "text"
	FieldNumber   FieldType = "number"
	FieldPassword FieldType = "password"
	FieldSelect   FieldType = "select"
	FieldBool     FieldType = "bool"
	FieldTextarea FieldType = "textarea"
)

// FieldOption select 类型的候选项。
type FieldOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Field 单个安装参数的表单声明。
type Field struct {
	Key         string        `json:"key"`                   // 与脚本读取的 shell 变量名一致
	Label       string        `json:"label"`                 // 表单标签（中文）
	Type        FieldType     `json:"type"`                  // 控件类型
	Required    bool          `json:"required,omitempty"`    // 是否必填
	Default     string        `json:"default,omitempty"`     // 默认值（空值回落）
	Placeholder string        `json:"placeholder,omitempty"` // 输入提示
	Help        string        `json:"help,omitempty"`        // 字段说明
	Pattern     string        `json:"pattern,omitempty"`     // 校验正则（Go 语法，可选）
	Min         *float64      `json:"min,omitempty"`         // number 下界（含）
	Max         *float64      `json:"max,omitempty"`         // number 上界（含）
	Options     []FieldOption `json:"options,omitempty"`     // select 候选项
	Advanced    bool          `json:"advanced,omitempty"`    // true 归入前端「高级」折叠区
}

// ValidateParams 按应用声明的 schema 校验并归一化安装参数，返回「字段键 → 最终值」。
//
// 归一化规则：空值回落到 Default；bool 归一为 "true"/"false"；number 归一为规范数字字符串。
// 未在 schema 中声明的键一律忽略——它们不会进入结果，也就不会被 RenderScript 拼进脚本，
// 从而杜绝「用任意键名当 shell 变量名注入」的路径。
// 返回的 error 文案为面向用户的中文，handler 可直接透出。
func ValidateParams(app App, in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(app.Fields))
	for _, f := range app.Fields {
		v := strings.TrimSpace(in[f.Key])
		if v == "" {
			v = f.Default // 空值回落到默认值
		}
		if v == "" {
			if f.Required {
				return nil, fmt.Errorf("参数「%s」不能为空", fieldLabel(f))
			}
			// 非必填且无默认：留空（脚本用自身的 := 默认兜底）
			continue
		}
		switch f.Type {
		case FieldNumber:
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("参数「%s」必须是数字", fieldLabel(f))
			}
			if f.Min != nil && n < *f.Min {
				return nil, fmt.Errorf("参数「%s」不能小于 %s", fieldLabel(f), trimNum(*f.Min))
			}
			if f.Max != nil && n > *f.Max {
				return nil, fmt.Errorf("参数「%s」不能大于 %s", fieldLabel(f), trimNum(*f.Max))
			}
			v = trimNum(n)
		case FieldBool:
			b, ok := parseBool(v)
			if !ok {
				return nil, fmt.Errorf("参数「%s」必须是布尔值", fieldLabel(f))
			}
			v = strconv.FormatBool(b)
		case FieldSelect:
			if !optionAllowed(f.Options, v) {
				return nil, fmt.Errorf("参数「%s」取值不在候选项中", fieldLabel(f))
			}
		case FieldText, FieldPassword, FieldTextarea:
			// 纯文本：仅做下面的可选正则校验
		default:
			return nil, fmt.Errorf("参数「%s」类型不受支持", fieldLabel(f))
		}
		if f.Pattern != "" {
			re, err := regexp.Compile(f.Pattern)
			if err != nil {
				// schema 配置错误（内置目录静态声明，正常不会发生）
				return nil, fmt.Errorf("参数「%s」的校验规则配置有误", fieldLabel(f))
			}
			if !re.MatchString(v) {
				return nil, fmt.Errorf("参数「%s」格式不正确", fieldLabel(f))
			}
		}
		out[f.Key] = v
	}
	return out, nil
}

// RenderScript 把校验后的参数前置成 shell 变量赋值，拼到安装脚本顶部。
// 参数为空时原样返回 Install（无 schema 应用与历史任务行为不变）。
// 值一律经 POSIX 单引号包裹（复用 vmssh.ShellQuote），杜绝把参数内容当 shell 语法执行。
func (a App) RenderScript(params map[string]string) string {
	if len(params) == 0 {
		return a.Install
	}
	var b strings.Builder
	b.WriteString("# 以下变量由平台按参数表单注入（用户提交值或 schema 默认值）\n")
	for _, f := range a.Fields { // 按声明顺序输出，结果稳定可复现
		v, ok := params[f.Key]
		if !ok {
			continue
		}
		b.WriteString(f.Key)
		b.WriteString("=")
		b.WriteString(vmssh.ShellQuote(v))
		b.WriteString("\n")
	}
	b.WriteString(a.Install)
	return b.String()
}

// fieldLabel 取字段展示名（缺省回落到 Key），用于中文错误文案。
func fieldLabel(f Field) string {
	if strings.TrimSpace(f.Label) != "" {
		return f.Label
	}
	return f.Key
}

// trimNum 去掉浮点数的多余尾零（8088.0 → "8088"，1.5 → "1.5"）。
func trimNum(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// parseBool 解析常见布尔写法（前端 el-switch 送 true/false，兼容 1/0、yes/no、on/off）。
func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	default:
		return false, false
	}
}

// optionAllowed 判断值是否在 select 候选项内。
func optionAllowed(opts []FieldOption, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

// floatPtr 取浮点数字面量地址（用于 Field.Min/Max 这类可选数值字段的声明式写法）。
func floatPtr(f float64) *float64 { return &f }
