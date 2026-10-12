package ansible

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jiuzhao/vmops/service/secretbox"
)

// TestParseSurvey 覆盖头部 survey 块的解析与向后兼容。
//
// 风险点：解析决定执行对话框渲染什么控件；且必须只看「首个非注释行之前」的注释块，
// 正文里的 # 注释不能被误当定义（否则用户随手写一行 # vmops-survey: 就能改语义）。
func TestParseSurvey(t *testing.T) {
	// 完整块：int + select（name/value 候选）+ 声明
	raw := []byte("# vmops-playbook: name=x | desc=y | targets=linux\n" +
		"# vmops-survey:\n" +
		"#   - name: port\n" +
		"#     type: int\n" +
		"#     default_value: \"8080\"\n" +
		"#     required: true\n" +
		"#   - name: env\n" +
		"#     type: select\n" +
		"#     values:\n" +
		"#       - {name: 生产, value: prod}\n" +
		"#       - {name: 测试, value: test}\n" +
		"---\n- hosts: all\n")
	vars, present, err := ParseSurvey(raw)
	if err != nil || !present {
		t.Fatalf("应解析出 survey：err=%v present=%v", err, present)
	}
	if len(vars) != 2 {
		t.Fatalf("变量数期望 2，实际 %d", len(vars))
	}
	if vars[0].Name != "port" || vars[0].Type != SurveyTypeInt || !vars[0].Required || vars[0].DefaultValue != "8080" {
		t.Errorf("port 定义解析不符：%+v", vars[0])
	}
	if vars[1].Name != "env" || vars[1].Type != SurveyTypeSelect || len(vars[1].Values) != 2 {
		t.Errorf("env 定义解析不符：%+v", vars[1])
	}
	if vars[1].Values[0].Name != "生产" || vars[1].Values[0].Value != "prod" {
		t.Errorf("select 候选解析不符：%+v", vars[1].Values[0])
	}

	// enum 标量候选
	enumRaw := []byte("# vmops-survey:\n#   - name: mode\n#     type: enum\n#     values: [dev, prod]\n---\n")
	ev, present, err := ParseSurvey(enumRaw)
	if err != nil || !present || len(ev) != 1 || len(ev[0].Values) != 2 {
		t.Fatalf("enum 标量候选解析失败：err=%v present=%v vars=%+v", err, present, ev)
	}
	if ev[0].Values[0].Name != "dev" || ev[0].Values[0].Value != "dev" {
		t.Errorf("enum 标量候选应 name==value：%+v", ev[0].Values[0])
	}

	// 无 survey 块 → present=false（调用方回落 vars= 清单）
	if _, present, _ := ParseSurvey([]byte("# vmops-playbook: name=x | vars=a,b\n---\n")); present {
		t.Error("无 survey 块时 present 应为 false")
	}

	// 正文里的同名注释不应被识别（扫描在首个非注释行停止）
	bodyOnly := []byte("---\n- hosts: all\n  # vmops-survey:\n  #   - name: x\n")
	if _, present, _ := ParseSurvey(bodyOnly); present {
		t.Error("正文中的注释不应被当作 survey 定义")
	}
}

// TestValidateSurvey 覆盖定义强校验的中文错误分支。
func TestValidateSurvey(t *testing.T) {
	ok := []SurveyVar{
		{Name: "port", Type: SurveyTypeInt, DefaultValue: "80"},
		{Name: "env", Type: SurveyTypeSelect, Values: []SurveyValue{{Name: "生产", Value: "prod"}}},
		{Name: "mode", Type: SurveyTypeEnum, Values: []SurveyValue{{Name: "dev", Value: "dev"}}},
		{Name: "pw", Type: SurveyTypeSecret},
		{Name: "note", Type: SurveyTypeText},
	}
	if err := ValidateSurvey(ok); err != nil {
		t.Fatalf("合法定义不应报错：%v", err)
	}

	cases := []struct {
		name string
		v    SurveyVar
	}{
		{"select 无候选值", SurveyVar{Name: "e", Type: SurveyTypeSelect}},
		{"enum 候选是对象", SurveyVar{Name: "e", Type: SurveyTypeEnum, Values: []SurveyValue{{Name: "生产", Value: "prod"}}}},
		{"int 默认值不可解析", SurveyVar{Name: "n", Type: SurveyTypeInt, DefaultValue: "abc"}},
		{"默认值不在候选集合", SurveyVar{Name: "e", Type: SurveyTypeEnum, Values: []SurveyValue{{Name: "dev", Value: "dev"}}, DefaultValue: "prod"}},
		{"未知类型", SurveyVar{Name: "x", Type: "float"}},
		{"非法变量名", SurveyVar{Name: "1bad", Type: SurveyTypeStr}},
		{"str 不应有候选值", SurveyVar{Name: "s", Type: SurveyTypeStr, Values: []SurveyValue{{Name: "a", Value: "a"}}}},
		{"非法 target", SurveyVar{Name: "t", Type: SurveyTypeStr, Target: "stdin"}},
	}
	for _, tc := range cases {
		if err := ValidateSurvey([]SurveyVar{tc.v}); err == nil {
			t.Errorf("%s：应报错但通过了", tc.name)
		}
	}
	// 重名
	if err := ValidateSurvey([]SurveyVar{{Name: "a", Type: SurveyTypeStr}, {Name: "a", Type: SurveyTypeStr}}); err == nil {
		t.Error("重名应报错")
	}
}

// TestValidateAnswers 覆盖答案校验（必填 / int / 候选集合）。
func TestValidateAnswers(t *testing.T) {
	vars := []SurveyVar{
		{Name: "port", Type: SurveyTypeInt, Required: true},
		{Name: "env", Type: SurveyTypeSelect, Values: []SurveyValue{{Name: "生产", Value: "prod"}}},
	}
	if err := ValidateAnswers(vars, map[string]string{"port": "8080", "env": "prod"}); err != nil {
		t.Errorf("合法答案不应报错：%v", err)
	}
	if err := ValidateAnswers(vars, map[string]string{"env": "prod"}); err == nil {
		t.Error("缺必填项应报错")
	}
	if err := ValidateAnswers(vars, map[string]string{"port": "abc"}); err == nil {
		t.Error("int 非数字应报错")
	}
	if err := ValidateAnswers(vars, map[string]string{"port": "80", "env": "stage"}); err == nil {
		t.Error("候选集合外的值应报错")
	}
}

// TestWriteExtraVarsFile 覆盖 @file 生成：字符串打 !unsafe、int 写裸数字、模板字符原样保留。
//
// 风险点：这是防 Jinja 模板注入的关键——用户填 `{{ lookup('pipe','rm -rf /') }}` 时，
// 若不打 !unsafe，ansible 会把它当模板求值。断言输出必须含 !unsafe 且原文未被破坏。
func TestWriteExtraVarsFile(t *testing.T) {
	dir := t.TempDir()
	vars := []SurveyVar{
		{Name: "port", Type: SurveyTypeInt},
		{Name: "name", Type: SurveyTypeStr},
	}
	answers := map[string]string{
		"port": "8080",
		"name": "{{ lookup('pipe','id') }}\nsecond",
	}
	p, err := WriteExtraVarsFile(dir, vars, answers)
	if err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读文件失败：%v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"port": 8080`) {
		t.Errorf("int 应写裸数字：\n%s", text)
	}
	if !strings.Contains(text, `"name": !unsafe "{{ lookup('pipe','id') }}\nsecond"`) {
		t.Errorf("字符串应打 !unsafe 且保留模板字符（转义换行）：\n%s", text)
	}
	// 非法 int 值应报错（不写坏文件）
	if _, err := WriteExtraVarsFile(dir, vars, map[string]string{"port": "x"}); err == nil {
		t.Error("int 收到非数字应报错")
	}
}

// TestApplySurveyAnswers 覆盖执行侧答案装配：secret 走环境变量（带前缀）、非 secret 走文件、
// target=env 走环境变量、无 survey 时全量落盘。
func TestApplySurveyAnswers(t *testing.T) {
	dir := t.TempDir()
	master := "test-master-key"

	// 写一个带 survey 的 playbook 文件
	pb := filepath.Join(dir, "pb.yml")
	content := "# vmops-playbook: name=x\n" +
		"# vmops-survey:\n" +
		"#   - name: region\n" +
		"#     type: str\n" +
		"#     target: env\n" +
		"#   - name: db_pass\n" +
		"#     type: secret\n" +
		"#   - name: port\n" +
		"#     type: int\n" +
		"---\n- hosts: all\n"
	if err := os.WriteFile(pb, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	enc, salt, err := secretbox.SealWithMaster(master, "s3cr3t")
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]interface{}{
		"extra_vars": map[string]interface{}{"region": "cn-north", "port": "8080"},
		"secret_vars": map[string]interface{}{
			"db_pass": map[string]interface{}{"enc": enc, "salt": salt},
		},
	}
	runDir := t.TempDir()
	extraFile, envVars, err := ApplySurveyAnswers(pb, runDir, payload, master)
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	// secret 与 target=env 走环境变量（带前缀），明文解密正确
	if envVars[SurveyEnvKey("db_pass")] != "s3cr3t" {
		t.Errorf("secret 应解密进环境变量：%v", envVars)
	}
	if envVars[SurveyEnvKey("region")] != "cn-north" {
		t.Errorf("target=env 应进环境变量：%v", envVars)
	}
	// 非 secret / 非 env 落盘为 @file
	if extraFile == "" {
		t.Fatal("port 应落盘为 extra-vars 文件")
	}
	raw, _ := os.ReadFile(extraFile)
	if !strings.Contains(string(raw), `"port": 8080`) {
		t.Errorf("int 答案应写裸数字：\n%s", raw)
	}
	if strings.Contains(string(raw), "s3cr3t") || strings.Contains(string(raw), "db_pass") {
		t.Errorf("secret 不应出现在 extra-vars 文件里：\n%s", raw)
	}

	// 无 survey 定义（旧 vars= 语义）：全部按字符串落盘
	legacy := filepath.Join(dir, "legacy.yml")
	if err := os.WriteFile(legacy, []byte("# vmops-playbook: name=old | vars=a,b\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p2 := map[string]interface{}{"extra_vars": map[string]interface{}{"a": "1", "b": "x"}}
	f2, env2, err := ApplySurveyAnswers(legacy, runDir, p2, master)
	if err != nil || f2 == "" || len(env2) != 0 {
		t.Fatalf("旧路径应全量落盘且无环境变量：file=%q env=%v err=%v", f2, env2, err)
	}
	raw2, _ := os.ReadFile(f2)
	if !strings.Contains(string(raw2), `"a": !unsafe "1"`) {
		t.Errorf("旧路径字符串也应打 !unsafe：\n%s", raw2)
	}
}
