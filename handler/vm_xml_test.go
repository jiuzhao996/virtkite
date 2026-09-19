package handler

import (
	"net/http"
	"testing"
)

// TestUpdateVMXMLRequiresAdmin 覆盖 XML 直定义端点的 admin 二次收口。
//
// 风险点：vms 路由组挂 OperatorMiddleware，operator 的写放行前缀恰是 /api/vms，
// 不加闸时 operator 可用任意 XML 整域改写虚拟机（磁盘/网卡/内存/任意设备）。
// 角色闸在 findVM 之前，本测试无需 DB/libvirt 依赖：非 admin 应在触碰 DB 前被拒。
func TestUpdateVMXMLRequiresAdmin(t *testing.T) {
	cases := []struct {
		name string
		role string
		set  bool // 是否写入 role（模拟中间件未写入的异常路径）
	}{
		{"operator 被拒", "operator", true},
		{"viewer 被拒", "viewer", true},
		{"角色缺失（未过鉴权中间件的防御路径）被拒", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newTestContext("/api/vms/1/xml", nil)
			if tc.set {
				c.Set("role", tc.role)
			}
			// 管理闸位于 findVM 之前，零值 handler 不会触碰 DB/Virt
			h := &VMHandler{}
			h.UpdateVMXML(c)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("期望 403，实际 %d，响应体=%s", rec.Code, rec.Body.String())
			}
			body := decodeBody(t, rec)
			if body["code"].(float64) != float64(http.StatusForbidden) {
				t.Errorf("业务 code 期望 403，实际 %v", body["code"])
			}
			msg, _ := body["message"].(string)
			if msg != "虚拟机 XML 直定义仅管理员可用" {
				t.Errorf("文案不符：%q", msg)
			}
		})
	}
}
