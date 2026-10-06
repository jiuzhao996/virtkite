package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestListAlertsEnvelope 实时告警接口的信封回归：此前裸透传 Alertmanager 数组，
// 前端 unwrap 后取 res.data 恒为 undefined（三处消费方实时告警一律空列表），
// 必须包成全站统一的 {code,message,data}。
func TestListAlertsEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/alerts" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"labels":{"alertname":"HighCPU"}},{"labels":{"alertname":"HostDown"}}]`))
	}))
	defer am.Close()

	listAlerts := func(url string) *httptest.ResponseRecorder {
		h := NewMonitorHandler(nil, url)
		req := httptest.NewRequest(http.MethodGet, "/api/monitor/alerts", nil)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = req
		h.ListAlerts(c)
		return rec
	}

	t.Run("正常透传=信封数组", func(t *testing.T) {
		rec := listAlerts(am.URL)
		if rec.Code != http.StatusOK {
			t.Fatalf("应 200，实际 %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Code int              `json:"code"`
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("响应不是合法信封 JSON: %v → %s", err, rec.Body.String())
		}
		if body.Code != 200 || len(body.Data) != 2 {
			t.Errorf("信封 code/data 不符: code=%d len(data)=%d", body.Code, len(body.Data))
		}
		if labels, ok := body.Data[0]["labels"].(map[string]any); !ok || labels["alertname"] != "HighCPU" {
			t.Errorf("首条告警内容丢失: %v", body.Data[0])
		}
	})

	t.Run("AM 空数组=信封空列表", func(t *testing.T) {
		empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[]`))
		}))
		defer empty.Close()
		rec := listAlerts(empty.URL)
		if rec.Code != http.StatusOK {
			t.Fatalf("应 200，实际 %d", rec.Code)
		}
		if got := rec.Body.String(); !json.Valid([]byte(got)) || got == `[]` {
			t.Errorf("空列表也应走信封，实际: %s", got)
		}
	})

	t.Run("AM 不可达=502", func(t *testing.T) {
		rec := listAlerts("http://127.0.0.1:1")
		if rec.Code != http.StatusBadGateway {
			t.Errorf("不可达应 502，实际 %d: %s", rec.Code, rec.Body.String())
		}
	})
}
