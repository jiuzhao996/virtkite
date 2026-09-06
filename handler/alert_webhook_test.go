package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
)

// alertWebhookRequest 构造 Alertmanager webhook v4 payload 并发起请求，返回响应记录。
func alertWebhookRequest(t *testing.T, h *AlertWebhookHandler, token string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("序列化 payload 失败: %v", err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/monitor/webhook", h.Handle)
	req := httptest.NewRequest(http.MethodPost, "/api/monitor/webhook", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestAlertWebhookToken 校验分支：Token 为空=公开；非空时要求 Bearer 匹配。
func TestAlertWebhookToken(t *testing.T) {
	const token = "s3cret"
	payload := map[string]interface{}{"version": "4", "alerts": []interface{}{}}

	t.Run("无令牌配置=公开", func(t *testing.T) {
		rec := alertWebhookRequest(t, &AlertWebhookHandler{}, "", payload)
		if rec.Code != http.StatusOK {
			t.Errorf("公开模式应 200，实际 %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("配置令牌后缺失鉴权=401", func(t *testing.T) {
		rec := alertWebhookRequest(t, &AlertWebhookHandler{Token: token}, "", payload)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("缺 Bearer 应 401，实际 %d", rec.Code)
		}
	})

	t.Run("令牌错误=401", func(t *testing.T) {
		rec := alertWebhookRequest(t, &AlertWebhookHandler{Token: token}, "wrong", payload)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("错误令牌应 401，实际 %d", rec.Code)
		}
	})

	t.Run("令牌正确=200", func(t *testing.T) {
		rec := alertWebhookRequest(t, &AlertWebhookHandler{Token: token}, token, payload)
		if rec.Code != http.StatusOK {
			t.Errorf("正确令牌应 200，实际 %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("query参数令牌=200", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.POST("/api/monitor/webhook", (&AlertWebhookHandler{Token: token}).Handle)
		data, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/monitor/webhook?token="+token, bytes.NewReader(data))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("?token= 应 200，实际 %d", rec.Code)
		}
	})
}

// TestAlertWebhookPayloadParsing HTTP 全链路 payload 解析（不依赖 DB：go.mod 无 sqlite
// driver，不新增依赖，fingerprint 非空的入库路径不在此测，纯逻辑见 TestConvertAlerts）。
// 这里只发 fingerprint 为空的告警——它们在进入 DB 之前就被过滤，stored 应为 0。
func TestAlertWebhookPayloadParsing(t *testing.T) {
	now := time.Now()
	payload := map[string]interface{}{
		"version": "4",
		"status":  "firing",
		"alerts": []map[string]interface{}{
			{
				"status":      "firing",
				"fingerprint": "",
				"labels":      map[string]string{"alertname": "VMRunningDrop", "severity": "warning"},
				"annotations": map[string]string{"summary": "虚拟机异常关机"},
				"startsAt":    now.Format(time.RFC3339),
			},
			{"status": "firing", "labels": map[string]string{"alertname": "NoFP"}},
		},
	}

	rec := alertWebhookRequest(t, &AlertWebhookHandler{}, "", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Received int `json:"received"`
			Stored   int `json:"stored"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v body=%s", err, rec.Body.String())
	}
	if resp.Data.Received != 2 {
		t.Errorf("received = %d, want 2", resp.Data.Received)
	}
	if resp.Data.Stored != 0 {
		t.Errorf("stored = %d, want 0（fingerprint 为空的应全部跳过）", resp.Data.Stored)
	}
}

// TestConvertAlerts convertAlerts 纯函数：过滤、状态归一、labels/annotations 序列化。
func TestConvertAlerts(t *testing.T) {
	starts := time.Date(2026, 9, 6, 10, 0, 0, 0, time.Local)
	ends := time.Date(2026, 9, 6, 11, 0, 0, 0, time.Local)
	alerts := []amAlert{
		{
			Status:      model.AlertStatusResolved,
			Fingerprint: "abc",
			Labels:      map[string]string{"alertname": "HostCpuHigh", "severity": "critical"},
			Annotations: nil,
			StartsAt:    starts,
			EndsAt:      &ends,
		},
		{Status: "firing", Fingerprint: "", Labels: map[string]string{"alertname": "Skip"}}, // 无指纹
	}

	out := convertAlerts(alerts)
	if len(out) != 1 {
		t.Fatalf("期望 1 条入库，实际 %d", len(out))
	}
	a := out[0]
	if a.Status != model.AlertStatusResolved {
		t.Errorf("status = %q, want resolved", a.Status)
	}
	if a.StartsAt != starts || a.EndsAt == nil || *a.EndsAt != ends {
		t.Errorf("时间字段不符: starts=%v ends=%v", a.StartsAt, a.EndsAt)
	}
	if a.Labels != `{"alertname":"HostCpuHigh","severity":"critical"}` {
		t.Errorf("labels 序列化不符: %s", a.Labels)
	}
	if a.Annotations != "{}" {
		t.Errorf("nil annotations 应序列化为 {}，实际 %s", a.Annotations)
	}

	// 异常状态归一为 firing
	odd := convertAlerts([]amAlert{{Status: "weird", Fingerprint: "x"}})
	if odd[0].Status != model.AlertStatusFiring {
		t.Errorf("异常状态应归一为 firing，实际 %q", odd[0].Status)
	}
}

// TestAlertWebhookBadBody 非法 JSON → 400（客户端错误可回 4xx，Alertmanager 自产 payload 恒合法）。
func TestAlertWebhookBadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/monitor/webhook", (&AlertWebhookHandler{}).Handle)
	req := httptest.NewRequest(http.MethodPost, "/api/monitor/webhook", bytes.NewReader([]byte("not-json")))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法 JSON 应 400，实际 %d", rec.Code)
	}
}
