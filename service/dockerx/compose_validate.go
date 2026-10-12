// compose 内容的权威校验（借 Dockge 的即时校验思路，但改走服务端真解析）。
//
// 为什么不在前端解析：本平台前端没有 YAML 库（刻意不新增依赖），且 JS 近似解析会误报；
// `docker compose -f <file> config` 才是 compose 自己的权威解析器，错误信息也最准。
//
// 临时文件：用 os.CreateTemp 落在系统临时目录（绝不写进任何池目录 / data 目录），
// defer os.Remove 清理。校验不执行任何构建/拉取，纯解析，故 30s 超时足够。
package dockerx

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ComposeIssue 一条校验错误（行号从 1 起；0 表示无法定位到具体行）。
type ComposeIssue struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// ComposeValidation 校验结果。Valid 为真时 Issues 为空。
type ComposeValidation struct {
	Valid   bool           `json:"valid"`
	Issues  []ComposeIssue `json:"issues"`
	Message string         `json:"message"`
}

// composeLineRe 从 compose 报错文本里抽取行号：形如 "yaml: line 3: xxx" 或 "line 3: xxx"。
var composeLineRe = regexp.MustCompile(`(?i)(?:yaml:\s*)?line (\d+):?\s*(.*)`)

// composeExitRe 剥掉 runTimeout 在错误串尾追加的 ": exit status N"。
var composeExitRe = regexp.MustCompile(`:?\s*exit status \d+\s*$`)

// ComposeValidate 校验 compose 内容（等价 docker compose -f <临时文件> config）。
// 返回的 error 仅表示「服务端内部失败」（临时文件读写），与「内容不合法」区分开：
// 内容不合法时 error 为 nil，由 ComposeValidation.Valid/Issues 表达。
func (d *Dockerx) ComposeValidate(content string) (ComposeValidation, error) {
	if strings.TrimSpace(content) == "" {
		return ComposeValidation{
			Valid:   false,
			Issues:  []ComposeIssue{{Line: 0, Message: "compose 内容为空"}},
			Message: "compose 内容为空",
		}, nil
	}
	f, err := os.CreateTemp("", "vmops-compose-*.yml")
	if err != nil {
		return ComposeValidation{}, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // 无论成败都清理
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return ComposeValidation{}, fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return ComposeValidation{}, fmt.Errorf("关闭临时文件失败: %w", err)
	}
	// -p 显式给项目名：否则 compose 会拿当前工作目录名当项目名，
	// 目录名含大写/点时会被判「项目名非法」，把校验结果带偏。
	if _, err := runTimeout(30*time.Second, "compose", "-p", "vmopsvalidate", "-f", tmp, "config"); err != nil {
		issues := parseComposeIssues(err.Error())
		msg := "compose 文件校验失败"
		if len(issues) > 0 && issues[0].Message != "" {
			msg = issues[0].Message
		}
		return ComposeValidation{Valid: false, Issues: issues, Message: msg}, nil
	}
	return ComposeValidation{Valid: true, Issues: []ComposeIssue{}, Message: "校验通过"}, nil
}

// parseComposeIssues 把 compose 的原始报错文本解析成结构化条目。
// 单行带行号的（yaml 语法错误）抽出行号；其余非空行归为「无法定位行」的通用错误。
func parseComposeIssues(text string) []ComposeIssue {
	issues := []ComposeIssue{}
	seen := map[string]bool{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(composeExitRe.ReplaceAllString(raw, ""))
		if line == "" {
			continue
		}
		issue := ComposeIssue{Line: 0, Message: line}
		if m := composeLineRe.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			msg := strings.TrimSpace(composeExitRe.ReplaceAllString(m[2], ""))
			if msg == "" {
				msg = line
			}
			issue = ComposeIssue{Line: n, Message: msg}
		}
		key := fmt.Sprintf("%d|%s", issue.Line, issue.Message)
		if seen[key] {
			continue
		}
		seen[key] = true
		issues = append(issues, issue)
	}
	if len(issues) == 0 {
		issues = append(issues, ComposeIssue{Line: 0, Message: "compose 文件校验失败"})
	}
	return issues
}
