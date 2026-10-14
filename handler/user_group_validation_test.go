package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestCreateGroupValidation 新建用户组在触碰 DB 前的纯校验分支。
//
// 风险点：组名先 binding:"required" 再 strings.TrimSpace 二次收口——「全是空白」的组名
// 能过 required 校验（非空字符串），必须靠 TrimSpace 兜住，否则会在库里落一个看不见的空名组，
// 前端列表里表现为一条无法识别的空白行。零值 UserGroupHandler（DB nil）能返回该 400
// 即证明 TrimSpace 判断先于查重/建组。
func TestCreateGroupValidation(t *testing.T) {
	h := &UserGroupHandler{} // DB 为 nil

	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"缺组名：binding required 拦截", `{}`, "参数错误"},
		{"非法 JSON", `{`, "参数错误"},
		{"纯空白组名：required 放行、TrimSpace 兜住", `{"name":"   "}`, "组名不能为空"},
		{"制表与换行也算空白", `{"name":"\t\n"}`, "组名不能为空"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := rawJSONCtx(http.MethodPost, "/api/user-groups", tc.body, nil)
			h.CreateGroup(c)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
			}
			body := decodeBody(t, rec)
			if msg, _ := body["message"].(string); !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("文案应含 %q，实际 %q", tc.wantMsg, msg)
			}
		})
	}
}

// TestUpdateGroupValidation 编辑用户组的纯校验分支（paramID + 空更新体）。
//
// 风险点：UpdateGroup 只有 description 一个可改字段，全缺省必须显式报错「没有可更新的字段」，
// 不能静默 200——前端会以为保存成功。另外主键仍走 paramID（防 GORM 内联注入）。
// 两条分支都先于 DB 查询 return，故零值 handler 能安全跑到。
func TestUpdateGroupValidation(t *testing.T) {
	h := &UserGroupHandler{} // DB 为 nil

	t.Run("非法主键被 paramID 拦", func(t *testing.T) {
		c, rec := newTestContext("/api/user-groups/x", gin.Params{{Key: "id", Value: "x"}})
		h.UpdateGroup(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("期望 400，实际 %d", rec.Code)
		}
	})

	t.Run("空更新体被拒", func(t *testing.T) {
		c, rec := rawJSONCtx(http.MethodPut, "/api/user-groups/3", `{}`,
			gin.Params{{Key: "id", Value: "3"}})
		h.UpdateGroup(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
		}
		body := decodeBody(t, rec)
		if msg, _ := body["message"].(string); msg != "没有可更新的字段" {
			t.Errorf("文案不符：%q", msg)
		}
	})
}

// TestUserGroupIDParsing 组相关端点的非法主键统一走 paramID（400）。
//
// 风险点：这是 group_id 进 GORM 前的唯一闸门（DELETE /:id、POST /:id/members）。
// 非法主键若漏判会以原始串拼进 WHERE，构成布尔盲注面。断言各端点都写 400 且 Abort。
func TestUserGroupIDParsing(t *testing.T) {
	h := &UserGroupHandler{}
	endpoints := []struct {
		name string
		call func(c *gin.Context)
	}{
		{"DeleteGroup", h.DeleteGroup},
		{"SetMembers", h.SetMembers},
	}
	for _, ep := range endpoints {
		for _, raw := range []string{"", "0", "abc", "1 OR 1=1"} {
			t.Run(ep.name+"/"+raw, func(t *testing.T) {
				c, rec := newTestContext("/api/user-groups/x", gin.Params{{Key: "id", Value: raw}})
				ep.call(c)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
				}
				if !c.IsAborted() {
					t.Error("非法主键未中断请求链")
				}
			})
		}
	}
}
