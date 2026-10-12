package handler

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPlaybookSurveyLegacyFallback 旧格式（只有 vars= 逗号清单、无 survey 块）必须回落为
// 全 str 定义——这是向后兼容的底线：不这么兜底，所有存量 playbook 的执行参数控件都会消失。
func TestPlaybookSurveyLegacyFallback(t *testing.T) {
	raw := []byte("# vmops-playbook: name=old | desc=d | targets=linux | vars=ops_user,admin_pubkey\n---\n- hosts: all\n")
	sv := playbookSurvey(raw, []string{"ops_user", "admin_pubkey"})
	if len(sv) != 2 {
		t.Fatalf("应回落出 2 个变量，实际 %d：%+v", len(sv), sv)
	}
	if sv[0].Name != "ops_user" || sv[0].Type != "str" {
		t.Errorf("旧格式变量应按 str 处理：%+v", sv[0])
	}
}

// TestPlaybookSurveyBlock survey 块优先于 vars= 清单；且带类型信息。
func TestPlaybookSurveyBlock(t *testing.T) {
	raw := []byte("# vmops-playbook: name=x | vars=ignored\n" +
		"# vmops-survey:\n" +
		"#   - name: port\n" +
		"#     type: int\n" +
		"#     required: true\n" +
		"---\n- hosts: all\n")
	sv := playbookSurvey(raw, []string{"ignored"})
	if len(sv) != 1 || sv[0].Name != "port" || sv[0].Type != "int" || !sv[0].Required {
		t.Fatalf("survey 块应优先且带类型：%+v", sv)
	}
}

// TestValidatePlaybookSurvey 保存/校验入口的定义强校验：非法块报中文错、无块放行。
func TestValidatePlaybookSurvey(t *testing.T) {
	// 无 survey 块 = 合法
	if err := validatePlaybookSurvey("---\n- hosts: all\n"); err != nil {
		t.Errorf("无 survey 块应放行：%v", err)
	}
	// 合法块
	good := "# vmops-survey:\n#   - name: n\n#     type: int\n#     default_value: \"1\"\n---\n"
	if err := validatePlaybookSurvey(good); err != nil {
		t.Errorf("合法 survey 块应通过：%v", err)
	}
	// select 缺候选值 → 报错
	bad := "# vmops-survey:\n#   - name: e\n#     type: select\n---\n"
	if err := validatePlaybookSurvey(bad); err == nil {
		t.Error("select 缺候选值应报错")
	}
}

// TestListPlaybooksIncludesSurvey 清单接口必须把 survey 定义带给前端（否则执行对话框
// 没有类型依据，只能退回裸输入框）。
func TestListPlaybooksIncludesSurvey(t *testing.T) {
	root := withTempPlaybookDir(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# vmops-playbook: name=survey-pb | desc=d | targets=linux\n" +
		"# vmops-survey:\n" +
		"#   - name: port\n" +
		"#     type: int\n" +
		"#     default_value: \"80\"\n" +
		"#   - name: env\n" +
		"#     type: select\n" +
		"#     values:\n" +
		"#       - {name: 生产, value: prod}\n" +
		"---\n- hosts: all\n"
	if err := os.WriteFile(filepath.Join(root, "survey-pb.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &AnsibleHandler{}
	c, rec := newTestContext("/api/ansible/playbooks", nil)
	h.ListPlaybooks(c)
	if rec.Code != 200 {
		t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	items, _ := body["data"].(map[string]interface{})["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(items))
	}
	m := items[0].(map[string]interface{})
	survey, _ := m["survey"].([]interface{})
	if len(survey) != 2 {
		t.Fatalf("survey 应有 2 项，实际 %d：%v", len(survey), survey)
	}
	first := survey[0].(map[string]interface{})
	if first["name"] != "port" || first["type"] != "int" || first["default_value"] != "80" {
		t.Errorf("int 变量定义不符：%v", first)
	}
	second := survey[1].(map[string]interface{})
	vals, _ := second["values"].([]interface{})
	if len(vals) != 1 {
		t.Fatalf("select 候选应有 1 项：%v", second)
	}
	v0 := vals[0].(map[string]interface{})
	if v0["name"] != "生产" || v0["value"] != "prod" {
		t.Errorf("select 候选不符：%v", v0)
	}
}
