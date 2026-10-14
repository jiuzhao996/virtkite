package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// rawJSONCtx 造一个带**原始字符串** body 的测试上下文。
// 与 jsonCtx 的差别：这里不 marshal，便于构造非法 JSON（jsonCtx 传字符串会把它 marshal 成合法 JSON 串）。
func rawJSONCtx(method, target, raw string, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	return c, rec
}

// TestUpdateSettingsValidation 系统设置写入在触碰 DB/Settings 之前的纯校验路径。
//
// 风险点：UpdateSettings 对每个可写键都是「先 setting.Validate 校验 → 不合法就 400 并 return；
// 合法才 h.Settings.Set 落库」。校验分支必须在 Set 之前 return——本测试用零值
// SettingsHandler（DB/Settings 均为 nil）跑这些分支，能跑到底恰恰证明它们是纯校验、
// 没提前解引用依赖，否则会 nil panic。同时「全部字段缺省」必须显式报错，不能静默 200
// 让前端以为保存成功（前端表单点保存却什么都没改是真实踩过的坑）。
func TestUpdateSettingsValidation(t *testing.T) {
	h := &SettingsHandler{} // DB/Settings 为 nil：只测不落库的校验分支

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantMsgSub string
	}{
		{"非法 JSON", `{`, http.StatusBadRequest, "参数错误"},
		{"空对象：没有任何字段", `{}`, http.StatusBadRequest, "没有需要更新的配置项"},
		{"默认存储池名含非法字符", `{"default_storage_pool":"bad name!"}`, http.StatusBadRequest, "默认存储池名不合法"},
		{"默认存储池名超长(65 位)", `{"default_storage_pool":"` + strings.Repeat("a", 65) + `"}`, http.StatusBadRequest, "默认存储池名不合法"},
		{"VNC token 有效期超上界(61)", `{"vnc_token_ttl_min":61}`, http.StatusBadRequest, "VNC token 有效期"},
		{"VNC token 有效期低于下界(0)", `{"vnc_token_ttl_min":0}`, http.StatusBadRequest, "VNC token 有效期"},
		{"VNC 过期时长超上界(1441)", `{"vnc_stale_min":1441}`, http.StatusBadRequest, "VNC 会话过期判定时长"},
		{"密码最小长度超上界(65)", `{"password_min_length":65}`, http.StatusBadRequest, "密码最小长度"},
		{"告警通知地址非 URL", `{"alert_notify_url":"notaurl"}`, http.StatusBadRequest, "告警通知地址"},
		{"安全入口口令含非法字符", `{"security_entrance":"a b"}`, http.StatusBadRequest, "安全入口口令"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := rawJSONCtx(http.MethodPut, "/api/settings", tc.body, nil)
			h.UpdateSettings(c)
			if rec.Code != tc.wantStatus {
				t.Fatalf("期望 %d，实际 %d，响应体=%s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			body := decodeBody(t, rec)
			msg, _ := body["message"].(string)
			if !strings.Contains(msg, tc.wantMsgSub) {
				t.Errorf("错误文案应含 %q，实际 %q", tc.wantMsgSub, msg)
			}
			// 校验失败文案是固定中文（无用户输入回显），不构成注入/反射面
			if strings.Contains(msg, "bad name!") || strings.Contains(msg, "notaurl") {
				t.Errorf("错误文案回显了用户输入：%q", msg)
			}
		})
	}
}
