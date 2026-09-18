package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// defaultLokiURL Loki 默认地址：宿主机原生直跑形态（compose 把 3100 绑在 127.0.0.1）。
// 全容器形态由 main 侧注入 LOKI_URL=http://loki:3100 覆盖。
const defaultLokiURL = "http://127.0.0.1:3100"

// lokiUnreachableMsg Loki 不可达时给前端的统一文案（完整错误进服务端日志）
const lokiUnreachableMsg = "Loki 未部署或不可达（参考 deploy/README-loki.md 部署）"

// defaultSince since 非法/缺省时的回退查询跨度
const defaultSince = time.Hour

// maxSince 查询跨度上限：与 Loki 保留期（loki-config.yml 的 retention_period=168h）对齐，
// 更早的日志已被 compactor 删除，放行只会得到空结果还拖累查询
const maxSince = 168 * time.Hour

// lokiQueryBodyLimit 透传响应体积上限（16MB）：limit 钳制在 5000 条内正常远小于该值，
// 仅防异常响应拖垮后端内存
const lokiQueryBodyLimit = 16 << 20

// LokiHandler 日志查询代理：前端（监控中心）的 LogQL 查询经后端转发给 Loki，
// 规避跨域，也避免暴露 Loki 地址与端口。Loki 未部署时返回 502 与部署指引，
// 不影响平台其余功能（日志栈是可选增量，见 deploy/README-loki.md）。
type LokiHandler struct {
	HTTP    *http.Client
	BaseURL string
}

// NewLokiHandler 创建日志查询代理（baseURL 来自 LOKI_URL，空值回退宿主机默认地址）。
func NewLokiHandler(baseURL string) *LokiHandler {
	if baseURL == "" {
		baseURL = defaultLokiURL
	}
	return &LokiHandler{
		HTTP:    &http.Client{Timeout: 15 * time.Second},
		BaseURL: baseURL,
	}
}

// Query GET /api/monitor/loki/query?query=<LogQL>&limit=100&since=1h
// 代理 Loki /loki/api/v1/query_range：since 换算为 start/end（纳秒），step 按跨度自动取点，
// Loki 原始响应 JSON 原样放进统一信封 data 字段（{code,message,data}）返回。
func (h *LokiHandler) Query(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		Fail(c, http.StatusBadRequest, "query 参数不能为空（示例：{container=~\"vmops-.*\"}）")
		return
	}

	since := parseSince(c.Query("since"))
	end := time.Now()
	start := end.Add(-since)
	q := url.Values{}
	q.Set("query", query)
	q.Set("limit", strconv.Itoa(clampLimit(c.Query("limit"))))
	q.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	q.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	q.Set("step", autoStep(since).String())

	body, err := h.getLoki(c, "/loki/api/v1/query_range?"+q.Encode(), lokiQueryBodyLimit)
	if err != nil {
		return // 响应已在 getLoki 内写好
	}
	// json.RawMessage：Loki 原始 JSON 作为信封的 data 值原样透传（不二次解构，字段演进零维护）
	Success(c, json.RawMessage(body))
}

// Labels GET /api/monitor/loki/labels → 代理 Loki /loki/api/v1/labels（标签名列表，
// 前端做 LogQL 选择器补全用），同样以统一信封包裹透传。
func (h *LokiHandler) Labels(c *gin.Context) {
	body, err := h.getLoki(c, "/loki/api/v1/labels", 1<<20)
	if err != nil {
		return
	}
	Success(c, json.RawMessage(body))
}

// getLoki 请求 Loki 并校验响应：成功返回原始响应体；失败写好 HTTP 响应并返回 error
// （调用方直接 return，不再重复写响应）。
func (h *LokiHandler) getLoki(c *gin.Context, pathAndQuery string, bodyLimit int64) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet,
		strings.TrimRight(h.BaseURL, "/")+pathAndQuery, nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "构造日志查询失败", err)
		return nil, err
	}

	resp, err := h.HTTP.Do(req)
	if err != nil {
		// 典型场景：日志栈未部署（docker compose -f deploy/docker-compose.loki.yml 未启动）
		ErrorWithMessage(c, http.StatusBadGateway, lokiUnreachableMsg,
			fmt.Errorf("Loki 不可达(%s): %w", h.BaseURL, err))
		return nil, err
	}
	defer resp.Body.Close()

	// 上限 bodyLimit：响应异常膨胀时不拖垮后端
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	if err != nil {
		ErrorWithMessage(c, http.StatusBadGateway, "读取 Loki 响应失败", err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// LogQL 语法错误等落在 400 档，细节进日志，前端只给状态码提示
		LogError(c, fmt.Errorf("Loki 查询失败: status=%d body=%s", resp.StatusCode, string(body)))
		Fail(c, http.StatusBadGateway, fmt.Sprintf("Loki 查询失败（HTTP %d，请检查 LogQL 语法）", resp.StatusCode))
		return nil, fmt.Errorf("loki status %d", resp.StatusCode)
	}
	// 原样透传走 json.RawMessage：非法 JSON 会在 gin 渲染期静默失败（客户端收到 200+空响应体），
	// 必须在校验合法后再透传（实测用例：夹具括号错位即触发此坑）
	if !json.Valid(body) {
		err := fmt.Errorf("Loki 响应不是合法 JSON: %.200s", string(body))
		ErrorWithMessage(c, http.StatusBadGateway, "Loki 响应格式异常", err)
		return nil, err
	}
	return body, nil
}

// parseSince 解析 since 参数为查询时间跨度（如 "1h" "30m" "2h30m"）。
// 非法（非 duration、≤0）回退 1h；超过 168h 钳制到 168h（与 Loki 保留期一致）。
func parseSince(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return defaultSince
	}
	if d > maxSince {
		return maxSince
	}
	return d
}

// clampLimit 解析 limit 参数：默认 100，范围 1~5000（Loki max_entries_limit_per_query 默认上限）。
func clampLimit(s string) int {
	const ( // 默认与上限拆开写，注释各自归位
		defaultLimit = 100
		maxLimit     = 5000
	)
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

// autoStep 按查询跨度自动定 step：目标约 240 个刻度（对齐 Grafana 默认密度），下限 1s。
// LogQL 日志查询忽略 step；指标查询（rate/sum 等）用它决定聚合粒度。
func autoStep(d time.Duration) time.Duration {
	step := d / 240
	if step < time.Second {
		return time.Second
	}
	return step.Truncate(time.Second)
}
