package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestDisconnectSessionInvalidID 强制断开会话的主键解析先于查库。
//
// 风险点：DisconnectSession 直接 strconv.ParseUint(c.Param("id"))，非法主键（空/非数字）
// 必须写 400 并在 h.DB.First 之前 return——零值 SessionHandler（DB/Sessions 均 nil）
// 返回 400 即证明未提前解引用依赖。这是会话强制断开入口，主键解析是进 GORM 的唯一闸门。
func TestDisconnectSessionInvalidID(t *testing.T) {
	h := &SessionHandler{} // DB/Sessions 为 nil
	for _, raw := range []string{"", "abc", "-1", "1.5", "1 OR 1=1"} {
		t.Run(raw, func(t *testing.T) {
			c, rec := newTestContext("/api/sessions/x/disconnect", gin.Params{{Key: "id", Value: raw}})
			h.DisconnectSession(c)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
			}
			if msg, _ := decodeBody(t, rec)["message"].(string); !strings.Contains(msg, "参数错误") {
				t.Errorf("文案不符：%q", msg)
			}
		})
	}
}

// TestSetNestedInvalidBody 嵌套虚拟化开关在触碰宿主机能力前先校验请求体。
//
// 风险点：SetNested 会写 /etc/modprobe.d 并尝试重载内核模块（宿主级副作用）。
// 请求体非法（非 JSON / enable 非布尔）必须在 hostkvm.Set 之前 return——本测试**只**
// 打非法体，正是为了在不产生宿主副作用的前提下证明校验前置。合法体不走此路径（会真写盘）。
func TestSetNestedInvalidBody(t *testing.T) {
	h := &HostHandler{} // 零值：非法体不应触达 hostkvm.Set
	cases := []struct {
		name string
		body string
	}{
		{"非法 JSON", `{`},
		{"enable 传字符串而非布尔", `{"enable":"yes"}`},
		{"enable 传数字", `{"enable":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := rawJSONCtx(http.MethodPost, "/api/host-kvm/nested", tc.body, nil)
			h.SetNested(c)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
			}
			if msg, _ := decodeBody(t, rec)["message"].(string); msg != "参数错误" {
				t.Errorf("文案不符：%q", msg)
			}
		})
	}
}
