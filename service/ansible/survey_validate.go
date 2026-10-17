// survey_validate.go：survey 定义强校验与用户答案校验（保存/执行前调用）。
package ansible

import (
	"fmt"
	"strconv"
	"strings"
)

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
