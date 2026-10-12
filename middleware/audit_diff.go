// audit_diff.go：审计的「变更前后快照」。
//
// 为什么要显式调用而不是让中间件自动抓：中间件只看得到 method/path，**没有对象上下文**。
// NetBox 那套靠 Django 的 post_save 信号自动快照，我们这套 Gin 中间件没有等价物——
// 硬套只会得到一堆空快照。所以由写操作在「改之前」和「改之后」各拍一张，经 SetAuditDiff
// 挂到请求上下文，中间件落库时取走。没调用过的操作两个字段留空，语义明确。
package middleware

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
)

const auditDiffKey = "audit_diff"

type auditDiff struct {
	Before string
	After  string
}

// auditSensitiveKeyParts 脱敏键（大小写不敏感的子串匹配）。
//
// 这条是硬要求，不是可选项：加了前后快照反而把口令写进审计表，比「没有快照」严重得多——
// 审计表是全站可查的只读记录，等于把凭据摊开给人看。任何名字里带这些片段的键，
// 值一律替换成 "***"。
var auditSensitiveKeyParts = []string{
	"password", "passwd", "secret", "token", "credential",
	"private_key", "ssh_key", "api_key", "master_key",
}

// SetAuditDiff 记录本次请求的对象变更前后快照。
// before / after 传任意可 JSON 序列化的值（结构体或 map 都行）；传 nil 表示该项留空。
//
// 只在**能拿到对象**的地方调用（service 层改规格/状态/授权的写操作），
// 拿不到对象的操作不要调用——留空比存一份无意义的快照诚实。
func SetAuditDiff(c *gin.Context, before, after interface{}) {
	if c == nil {
		return
	}
	c.Set(auditDiffKey, auditDiff{
		Before: marshalRedacted(before),
		After:  marshalRedacted(after),
	})
}

// marshalRedacted 序列化并脱敏；序列化失败返回空串（审计快照缺失不该影响主流程）。
func marshalRedacted(v interface{}) string {
	if v == nil {
		return ""
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var any interface{}
	if err := json.Unmarshal(raw, &any); err != nil {
		return ""
	}
	masked, err := json.Marshal(redactValue(any))
	if err != nil {
		return ""
	}
	return string(masked)
}

// redactValue 递归遍历 JSON 值，把敏感键的值替换成 "***"。
func redactValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			if isSensitiveKey(k) {
				out[k] = "***"
				continue
			}
			out[k] = redactValue(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = redactValue(val)
		}
		return out
	default:
		return v
	}
}

func isSensitiveKey(k string) bool {
	lower := strings.ToLower(k)
	for _, part := range auditSensitiveKeyParts {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

// takeAuditDiff 取出并清空本请求的快照（中间件落库时调用）。
func takeAuditDiff(c *gin.Context) (before, after string) {
	v, ok := c.Get(auditDiffKey)
	if !ok {
		return "", ""
	}
	d, ok := v.(auditDiff)
	if !ok {
		return "", ""
	}
	return d.Before, d.After
}
