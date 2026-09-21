package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSendTextOK 200 成功：断言请求方法、Content-Type 与 payload 结构。
func TestSendTextOK(t *testing.T) {
	var gotBody map[string]any
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &gotBody); err != nil {
			t.Errorf("payload 非合法 JSON: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := SendText(srv.URL, "[鸢航 VirtKite] 测试消息"); err != nil {
		t.Fatalf("200 应成功，实际报错: %v", err)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if gotBody["msg_type"] != "text" {
		t.Errorf("msg_type = %v, want text", gotBody["msg_type"])
	}
	content, ok := gotBody["content"].(map[string]any)
	if !ok {
		t.Fatalf("content 结构不符: %v", gotBody["content"])
	}
	if content["text"] != "[鸢航 VirtKite] 测试消息" {
		t.Errorf("text = %v, want 原文透传", content["text"])
	}
}

// TestSendTextNon2xx 非 200 状态码应报错（机器人 token 非法时飞书/钉钉返回 4xx/5xx）。
func TestSendTextNon2xx(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		err := SendText(srv.URL, "hi")
		srv.Close()
		if err == nil {
			t.Fatalf("状态码 %d 应报错", status)
		}
		if !strings.Contains(err.Error(), "状态码") {
			t.Errorf("状态码 %d 的错误应包含状态码信息，实际: %v", status, err)
		}
	}
}

// TestSendTextBadURL URL 非法（解析失败/协议不支持）应报错而非 panic。
func TestSendTextBadURL(t *testing.T) {
	for _, url := range []string{"", "://no-scheme", "not a url"} {
		if err := SendText(url, "hi"); err == nil {
			t.Errorf("URL %q 应报错", url)
		}
	}
}

// TestSendTextUnreachable 目标不可达应报错（连接拒绝），且是 %w 包装可继续判断。
func TestSendTextUnreachable(t *testing.T) {
	// 先起一个真实端口再关掉，保证地址必然连接拒绝
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close()
	if err := SendText(url, "hi"); err == nil {
		t.Error("不可达地址应报错")
	}
}
