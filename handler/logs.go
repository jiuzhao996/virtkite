package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// LogHandler 平台日志查询：只读代理 VictoriaLogs 的 LogsQL（内置「日志」页用）。
// 前端不直接连 VictoriaLogs——后者无鉴权且绑回环，经平台代理还能复用现有 RBAC 与统一响应。
type LogHandler struct {
	VLURL  string
	Client *http.Client
}

func NewLogHandler(vlURL string) *LogHandler {
	return &LogHandler{
		VLURL:  strings.TrimRight(vlURL, "/"),
		Client: &http.Client{Timeout: 10 * time.Second},
	}
}

const (
	logQueryMaxLen   = 1024 // LogsQL 表达式长度上限
	logQueryMaxLimit = 500  // 单次返回条数上限
	logQueryDefLimit = 100  // 默认条数
)

// Query GET /api/logs/query?query=..&limit=..
// query 为 LogsQL（缺省 * = 全部）；limit 钳到 [1,500]，默认 100。
// 返回 {items:[{_time,_msg,source_type,container_name,...}], count, source}。
// VictoriaLogs 不可达时降级为 200 + source=unavailable（与监控曲线同款优雅降级），不报 5xx。
func (h *LogHandler) Query(c *gin.Context) {
	if h.VLURL == "" {
		Success(c, gin.H{"items": []any{}, "source": "disabled", "message": "未配置 VictoriaLogs（VICTORIALOGS_URL）"})
		return
	}
	q := strings.TrimSpace(c.Query("query"))
	if q == "" {
		q = "*"
	}
	if len(q) > logQueryMaxLen {
		Fail(c, http.StatusBadRequest, "查询表达式过长（上限 1024 字符）")
		return
	}
	limit := logQueryDefLimit
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 {
		limit = n
	}
	if limit > logQueryMaxLimit {
		limit = logQueryMaxLimit
	}

	params := url.Values{}
	params.Set("query", q)
	params.Set("limit", strconv.Itoa(limit))
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet,
		h.VLURL+"/select/logsql/query?"+params.Encode(), nil)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		LogError(c, fmt.Errorf("日志后端 VictoriaLogs 不可达: %w", err))
		Success(c, gin.H{"items": []any{}, "count": 0, "source": "unavailable", "message": "日志后端不可达（VictoriaLogs 未启动？）"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		LogError(c, fmt.Errorf("VictoriaLogs 响应异常: %s", resp.Status))
		Success(c, gin.H{"items": []any{}, "count": 0, "source": "unavailable", "message": "日志后端响应异常"})
		return
	}

	// VictoriaLogs 返回 NDJSON：每行一个日志对象的 JSON。逐行解析，坏行跳过（一行坏不毁整次查询）。
	items := make([]map[string]any, 0, limit)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 单行日志可能很长，缓冲抬到 1MB
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		items = append(items, m)
	}
	Success(c, gin.H{"items": items, "count": len(items), "source": "victorialogs"})
}
