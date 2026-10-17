// tasklog_parse.go：任务日志条目的解析与进度噪音行判定。
package tasks

import (
	"encoding/json"
	"strings"

	"github.com/jiuzhao/vmops/model"
)

// ParseLogEntries 解析 log_entries 列：成功返回条目数组与 true；
// 空串或非 JSON 数组（老任务的纯文本 result 被误传进来）返回 false，调用方回退纯文本渲染。
// 导出供 handler（GET 日志 / 日志 WS）复用，保证前后端「结构化 vs 纯文本」判定口径一致。
func ParseLogEntries(raw string) ([]model.TaskLogEntry, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw[0] != '[' {
		return nil, false
	}
	var entries []model.TaskLogEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, false
	}
	return entries, true
}

// noiseLogSubstrings docker build / pull 过程中的进度噪音片段（小写匹配）：
// 这类行数量大、信息量低，默认折叠成调试行，避免真实构建输出被淹没。
var noiseLogSubstrings = []string{
	"extracting sha256:",
	"pulling fs layer",
	"verifying checksum",
	"download complete",
	"already exists",
	"waiting",
}

// isNoiseLogLine 启发式判断是否为可折叠的进度噪音行。best-effort：误判只影响默认折叠与否，
// 用户可一键展开，不会丢数据。错误行通常不含这些片段。
func isNoiseLogLine(text string) bool {
	lower := strings.ToLower(text)
	for _, sub := range noiseLogSubstrings {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}
