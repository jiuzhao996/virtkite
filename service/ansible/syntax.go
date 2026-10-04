// syntax.go：playbook 语法校验（ansible-playbook --syntax-check）。
// 保存/更新 playbook 前调用，报错尽量带回 ansible 的原始行号信息。
package ansible

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// syntaxErrRe 匹配 ansible 报错中的位置信息，如 `... (line: 12, column: 3)`。
var syntaxErrRe = regexp.MustCompile(`\(line:\s*(\d+)[^)]*\)`)

// SyntaxCheck 校验 playbook YAML 内容。返回 nil=通过；非 nil=错误（含行号）。
// 校验用空 inventory（localhost,）：语法检查不连主机，只是让 CLI 不报缺 inventory。
func SyntaxCheck(content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("playbook 内容为空")
	}
	// 惯例：首个非空非注释行应是文档分隔符 ---（元数据注释头在其前是合法的）
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if t != "---" {
			return errors.New("playbook 应以 --- 开头（元数据注释行可在此前）")
		}
		break
	}
	eng, err := Detect()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "vmops-pbcheck-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(dir)
	f := filepath.Join(dir, "playbook.yml")
	if werr := os.WriteFile(f, []byte(content), 0o600); werr != nil {
		return fmt.Errorf("写临时文件失败: %w", werr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := execCombined(ctx, eng.PlaybookPath, "--syntax-check", "-i", "localhost,", f)
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(out)
	if msg == "" {
		msg = err.Error()
	}
	// ansible 的报错块形如 "ERROR! ... syntax error at line 12"；原样回传前端（首 500 字）
	if line := syntaxErrRe.FindStringSubmatch(out); line != nil {
		return fmt.Errorf("语法错误（第 %s 行附近）: %s", line[1], truncateStr(msg, 500))
	}
	return errors.New(truncateStr("语法校验未通过: "+msg, 500))
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
