package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestPasswordPolicyDefault 未接线设置管理器时的密码策略兜底。
//
// 风险点：passwordPolicy 在 settingMgr 为 nil 时返回改造前的硬编码下限 6。
// 这是「忘记注入管理器」场景下的安全兜底——若哪天被改成返回 0（等于策略关闭），
// 建用户/改密的口令强度约束会静默消失，且没有任何报错提示，属最难发现的回归。
func TestPasswordPolicyDefault(t *testing.T) {
	h := &UserHandler{} // settingMgr 未注入
	if got := h.passwordPolicy(); got != legacyPasswordMinLength {
		t.Errorf("未注入设置管理器时应退回硬底线 %d，实际 %d", legacyPasswordMinLength, got)
	}
	if legacyPasswordMinLength <= 0 {
		t.Errorf("硬底线必须为正（否则等于策略关闭），实际 %d", legacyPasswordMinLength)
	}
}

// TestValidRoles 角色白名单必须是且仅是 admin/operator/viewer。
//
// 风险点：CreateUser/UpdateUser 用 validRoles 做角色合法性闸门。多一个键=多一种可提权角色；
// 少一个键=合法角色被拒。这里钉死全集，防被顺手加进来路不明的角色值。
func TestValidRoles(t *testing.T) {
	want := map[string]bool{"admin": true, "operator": true, "viewer": true}
	if len(validRoles) != len(want) {
		t.Fatalf("角色白名单项数应为 %d，实际 %d：%v", len(want), len(validRoles), validRoles)
	}
	for k := range want {
		if !validRoles[k] {
			t.Errorf("合法角色 %q 缺失", k)
		}
	}
	for _, bad := range []string{"", "Admin", "root", "superuser", "admin "} {
		if validRoles[bad] {
			t.Errorf("角色 %q 不应在白名单内", bad)
		}
	}
}

// TestCreateUserValidation CreateUser 在触碰 DB 之前的纯校验分支。
//
// 风险点：CreateUser 用零值 UserHandler（DB 为 nil）能跑到最后说明这些分支没提前解引用 DB。
// 关键是「弱口令」和「非法角色」都必须在 HashPassword/Create 之前就被拒——若顺序写反，
// 弱口令会先被哈希甚至写库，策略等于形同虚设。
func TestCreateUserValidation(t *testing.T) {
	h := &UserHandler{} // DB/settingMgr 为 nil，只测不落库的分支

	cases := []struct {
		name       string
		body       interface{}
		wantStatus int
		wantMsg    string
	}{
		{"缺 username/password：绑定失败", map[string]string{}, http.StatusBadRequest, "参数错误"},
		{"密码过短（低于硬底线 6）", map[string]string{"username": "bob", "password": "123"}, http.StatusBadRequest, "密码长度"},
		{"角色不在白名单", map[string]string{"username": "bob", "password": "abcdef", "role": "root"}, http.StatusBadRequest, "角色不合法"},
		{"角色大小写敏感：Admin 被拒", map[string]string{"username": "bob", "password": "abcdef", "role": "Admin"}, http.StatusBadRequest, "角色不合法"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := jsonCtx(t, http.MethodPost, "/api/users", nil, tc.body)
			h.CreateUser(c)
			if rec.Code != tc.wantStatus {
				t.Fatalf("期望 %d，实际 %d，响应体=%s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			body := decodeBody(t, rec)
			msg, _ := body["message"].(string)
			if !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("错误文案应含 %q，实际 %q", tc.wantMsg, msg)
			}
		})
	}
}

// TestDeleteUserCannotDeleteSelf 删除用户的自删守卫在触碰 DB 之前生效。
//
// 风险点：DeleteUser 先 paramID 解析、再从上下文取当前用户 id，命中「删的是自己」就 400 return。
// 该分支必须在 DB.First 之前——零值 UserHandler（DB nil）能跑到底即证明之。
// 若把自删判断挪到查库之后，配合「管理员误点自己的删除按钮」会真的把当前账号删掉、当场锁死。
func TestDeleteUserCannotDeleteSelf(t *testing.T) {
	h := &UserHandler{} // DB 为 nil

	t.Run("删自己被拒", func(t *testing.T) {
		c, rec := newTestContext("/api/users/5", gin.Params{{Key: "id", Value: "5"}})
		c.Set("user_id", uint(5))
		h.DeleteUser(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
		}
		body := decodeBody(t, rec)
		if msg, _ := body["message"].(string); msg != "不能删除自己" {
			t.Errorf("文案不符：%q", msg)
		}
	})

	t.Run("主键非法先于一切被拦", func(t *testing.T) {
		c, rec := newTestContext("/api/users/x", gin.Params{{Key: "id", Value: "x"}})
		h.DeleteUser(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("期望 400，实际 %d", rec.Code)
		}
		if !c.IsAborted() {
			t.Error("非法主键未中断请求链")
		}
	})
}

// TestChangeMyPasswordRequiresLogin 未登录（上下文无 user_id）时先 401。
//
// 风险点：ChangeMyPassword 取不到 user_id 必须直接 401，不能带着 nil 去查库——
// 若漏判，h.DB.First(&me, nil) 的语义未定义（可能捞出任意一行），等于把「改密码」
// 挂到了错误对象上。零值 UserHandler（DB nil）能返回 401 即证明此处未解引用 DB。
func TestChangeMyPasswordRequiresLogin(t *testing.T) {
	h := &UserHandler{}
	c, rec := jsonCtx(t, http.MethodPost, "/api/users/me/password", nil,
		map[string]string{"old_password": "a", "new_password": "b"})
	h.ChangeMyPassword(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d，响应体=%s", rec.Code, rec.Body.String())
	}
}
