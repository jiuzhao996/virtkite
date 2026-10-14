package handler

import (
	"net/http"
	"strings"
	"testing"
)

// TestActionLabelsInvariants 审计操作类型 → 中文文案映射的数据不变式。
//
// 风险点：ActionLabels 同时被审计列表展示与 CSV 导出（label 缺失时回退原始 action）消费。
// 空 key / 空 value 会产出「有操作却显示空白」的审计行，等于审计留痕失效；核心动作键
// （登录、三类 VM 生命期）缺失会让最常见的操作在页面上退化成英文代号。这里钉死下限不变式。
func TestActionLabelsInvariants(t *testing.T) {
	if len(ActionLabels) == 0 {
		t.Fatal("ActionLabels 不能为空")
	}
	for k, v := range ActionLabels {
		if strings.TrimSpace(k) == "" {
			t.Errorf("存在空 key 的映射项")
		}
		if strings.TrimSpace(v) == "" {
			t.Errorf("动作 %q 的中文文案为空", k)
		}
	}

	// 核心动作必须覆盖（determineAction 稳定产出这些；缺失即展示退化）
	for _, must := range []string{"login", "create_vm", "delete_vm", "start_vm", "stop_vm", "access"} {
		if _, ok := ActionLabels[must]; !ok {
			t.Errorf("核心动作 %q 未登记中文文案", must)
		}
	}
}

// TestListAuditActionsEndpoint GET /api/audit/actions 直接回映射表，不触碰 DB。
//
// 风险点：该端点无 DB 依赖（纯回 ActionLabels），零值 AuditHandler 即可断言；
// 若某天被改成查库，本用例会因 nil DB panic 而暴露，等于顺手锁住了「无依赖」这个约定。
func TestListAuditActionsEndpoint(t *testing.T) {
	h := &AuditHandler{} // DB 为 nil：本端点不应查库
	c, rec := newTestContext("/api/audit/actions", nil)
	h.ListAuditActions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d，响应体=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data 应为对象，实际 %T", body["data"])
	}
	if len(data) != len(ActionLabels) {
		t.Errorf("回传项数 %d 与 ActionLabels(%d) 不一致", len(data), len(ActionLabels))
	}
	if got, _ := data["login"].(string); got != ActionLabels["login"] {
		t.Errorf("login 文案不符：%q", got)
	}
}
