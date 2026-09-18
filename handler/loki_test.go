package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestParseSince since 参数解析：合法 duration 直取，非法/非正值回退 1h，超保留期钳到 168h。
func TestParseSince(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"1h 直取", "1h", time.Hour},
		{"30m 直取", "30m", 30 * time.Minute},
		{"复合写法 2h30m", "2h30m", 2*time.Hour + 30*time.Minute},
		{"空串回退默认 1h", "", defaultSince},
		{"非法字符串回退默认", "abc", defaultSince},
		{"纯数字不是合法 duration 回退默认", "3600", defaultSince},
		{"负值回退默认", "-5m", defaultSince},
		{"零值回退默认", "0", defaultSince},
		{"超过保留期钳制到 168h", "1000h", maxSince},
		{"恰好 168h 不变", "168h", maxSince},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseSince(c.in); got != c.want {
				t.Errorf("parseSince(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestClampLimit limit 参数钳制：默认 100，范围 1~5000。
func TestClampLimit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"缺省回默认 100", "", 100},
		{"非法回默认", "abc", 100},
		{"0 回默认", "0", 100},
		{"负数回默认", "-1", 100},
		{"1 保下界", "1", 1},
		{"50 直取", "50", 50},
		{"超上限钳 5000", "9999", 5000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampLimit(c.in); got != c.want {
				t.Errorf("clampLimit(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// TestAutoStep step 自动取点：约 240 刻度、下限 1s。
func TestAutoStep(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"1h → 15s（3600s/240）", time.Hour, 15 * time.Second},
		{"168h → 42m", 168 * time.Hour, 42 * time.Minute},
		{"30m → 7s（7.5s 截断）", 30 * time.Minute, 7 * time.Second},
		{"10m → 2s（2.5s 截断）", 10 * time.Minute, 2 * time.Second},
		{"1s 保下限", time.Second, time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := autoStep(c.in); got != c.want {
				t.Errorf("autoStep(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestNewLokiHandlerDefaultURL 空地址回退宿主机默认值（LOKI_URL 未配置的场景）。
func TestNewLokiHandlerDefaultURL(t *testing.T) {
	if got := NewLokiHandler("").BaseURL; got != defaultLokiURL {
		t.Errorf("NewLokiHandler(\"\").BaseURL = %q, want %q", got, defaultLokiURL)
	}
	if got := NewLokiHandler("http://loki:3100").BaseURL; got != "http://loki:3100" {
		t.Errorf("显式地址应原样保留，got %q", got)
	}
}

// newLokiTestRouter 独立 gin 引擎挂 Query/Labels（与 routes.go 的挂载路径同级语义）。
func newLokiTestRouter(h *LokiHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/monitor/loki/query", h.Query)
	r.GET("/api/monitor/loki/labels", h.Labels)
	return r
}

// TestLokiQueryMissingParam 缺 query 参数必须 400（防全表扫描式空查询打到 Loki）。
func TestLokiQueryMissingParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := newLokiTestRouter(NewLokiHandler("http://127.0.0.1:1"))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/loki/query?limit=10", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺 query 期望 400，got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["code"].(float64) != 400 {
		t.Errorf("信封 code 期望 400，got %v", body["code"])
	}
}

// TestLokiQueryProxy 走真实 httptest 上游验证代理链路：
// 参数换算正确、Loki 原始 JSON 被原样放进统一信封 data 字段。
func TestLokiQueryProxy(t *testing.T) {
	lokiBody := `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"container":"vmops-app"},"values":[["1726000000000000000","line"]]}]},"stats":{}}`
	var upstreamQuery, upstreamLimit, upstreamStep string
	var upstreamStart, upstreamEnd int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" {
			t.Errorf("上游路径不符: %s", r.URL.Path)
		}
		q := r.URL.Query()
		upstreamQuery = q.Get("query")
		upstreamLimit = q.Get("limit")
		upstreamStep = q.Get("step")
		upstreamStart, _ = strconv.ParseInt(q.Get("start"), 10, 64)
		upstreamEnd, _ = strconv.ParseInt(q.Get("end"), 10, 64)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(lokiBody))
	}))
	defer upstream.Close()

	r := newLokiTestRouter(NewLokiHandler(upstream.URL))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/loki/query?query="+
		url.QueryEscape(`{container=~"vmops-.*"}`)+"&since=2h&limit=50", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，got %d body=%s", w.Code, w.Body.String())
	}
	// 上游收到的参数
	if upstreamQuery != `{container=~"vmops-.*"}` {
		t.Errorf("LogQL 未透传: %q", upstreamQuery)
	}
	if upstreamLimit != "50" {
		t.Errorf("limit 未透传: %q", upstreamLimit)
	}
	if upstreamStep == "" {
		t.Error("step 未设置")
	}
	// since=2h → start/end 纳秒且跨度 2h（留 2s 时钟误差）
	span := time.Duration(upstreamEnd-upstreamStart) * time.Nanosecond
	if span < 2*time.Hour-2*time.Second || span > 2*time.Hour+2*time.Second {
		t.Errorf("start/end 跨度期望约 2h，got %v", span)
	}
	// 信封包裹 + data 原样透传
	var body struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Code != 200 || body.Message != "success" {
		t.Errorf("信封异常: code=%d message=%q", body.Code, body.Message)
	}
	if string(body.Data) != lokiBody {
		t.Errorf("data 未原样透传 Loki 响应:\n got  %s\n want %s", body.Data, lokiBody)
	}
}

// TestLokiQueryUnreachable 上游不可达（日志栈未部署）必须 502 + 中文部署指引。
func TestLokiQueryUnreachable(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // 立即关闭：端口必然拒绝连接

	r := newLokiTestRouter(NewLokiHandler(deadURL))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/loki/query?query="+url.QueryEscape(`{job="x"}`), nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("不可达期望 502，got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	msg, _ := body["message"].(string)
	if !containsCJK(msg) || !strings.Contains(msg, "Loki 未部署或不可达") {
		t.Errorf("502 文案应包含部署指引，got %q", msg)
	}
}

// TestLokiQueryInvalidUpstream 上游返回非法 JSON 时必须 502 而非 200+空响应体
// （json.RawMessage 透传非法 JSON 会在 gin 渲染期静默失败，getLoki 用 json.Valid 兜底）。
func TestLokiQueryInvalidUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success", broken`))
	}))
	defer upstream.Close()

	r := newLokiTestRouter(NewLokiHandler(upstream.URL))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/loki/query?query="+url.QueryEscape("{job=\"x\"}"), nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("非法上游响应期望 502，got %d body=%q", w.Code, w.Body.String())
	}
}

// TestLokiLabelsProxy labels 接口同样代理并信封包裹。
func TestLokiLabelsProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/labels" {
			t.Errorf("上游路径不符: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":["container","job","stream"]}`))
	}))
	defer upstream.Close()

	r := newLokiTestRouter(NewLokiHandler(upstream.URL))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/loki/labels", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，got %d", w.Code)
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Status string   `json:"status"`
			Labels []string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Code != 200 || body.Data.Status != "success" || len(body.Data.Labels) != 3 {
		t.Errorf("labels 透传异常: %+v", body)
	}
}
