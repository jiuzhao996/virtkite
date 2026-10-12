package dockerx

import "testing"

// TestParseComposeIssues 校验错误文本解析：抽出行号、剥离 exit status 尾巴、去重。
func TestParseComposeIssues(t *testing.T) {
	t.Run("yaml 语法错误带行号", func(t *testing.T) {
		issues := parseComposeIssues("yaml: line 3: did not find expected key: exit status 1")
		if len(issues) != 1 {
			t.Fatalf("期望 1 条，实际 %d：%+v", len(issues), issues)
		}
		if issues[0].Line != 3 || issues[0].Message != "did not find expected key" {
			t.Errorf("解析错误：%+v", issues[0])
		}
	})

	t.Run("schema 错误无行号", func(t *testing.T) {
		issues := parseComposeIssues("services.web.ports must be a list\nexit status 1")
		if len(issues) != 1 {
			t.Fatalf("期望 1 条（exit status 行应被剥除），实际 %d：%+v", len(issues), issues)
		}
		if issues[0].Line != 0 || issues[0].Message != "services.web.ports must be a list" {
			t.Errorf("解析错误：%+v", issues[0])
		}
	})

	t.Run("空文本兜底", func(t *testing.T) {
		issues := parseComposeIssues("")
		if len(issues) != 1 || issues[0].Line != 0 {
			t.Errorf("空文本应有兜底错误：%+v", issues)
		}
	})
}

// TestComposeValidateEmpty 空内容不落临时文件、不起进程，直接返回不合法。
func TestComposeValidateEmpty(t *testing.T) {
	res, err := New().ComposeValidate("   \n  ")
	if err != nil {
		t.Fatalf("空内容不应返回内部错误: %v", err)
	}
	if res.Valid {
		t.Error("空内容不应判为合法")
	}
	if len(res.Issues) == 0 || res.Issues[0].Message == "" {
		t.Errorf("空内容应有错误条目：%+v", res)
	}
}
