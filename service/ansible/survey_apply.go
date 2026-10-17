// survey_apply.go：survey 答案的执行期注入——extra-vars 文件生成、secret 解密与环境变量装配。
package ansible

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jiuzhao/vmops/service/secretbox"
)

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
