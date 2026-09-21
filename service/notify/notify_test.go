package notify

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loopbackClient 构造「连接改拨环回」的 client：URL 可写任意域名（如钉钉的
// oapi.dingtalk.com，无法注册为 httptest 地址），连接实际打到 srv 的环回端口，
// 从而对真实发出的报文做断言（供 send 的 client 注入参数使用）。
func loopbackClient(t *testing.T, srvURL string) *http.Client {
	t.Helper()
	addr := strings.TrimPrefix(srvURL, "http://")
	return &http.Client{
		Timeout: sendTimeout,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, network, _ string) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
	}
}

// TestSendTextOK 200 成功：断言请求方法、Content-Type 与 payload 结构。
// 非 dingtalk 域名一律走飞书格式，此用例同时是飞书路径的回归锚。
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

// TestSendTextDingTalkPayload 钉钉域名分派：URL host 含 oapi.dingtalk.com 时应发
// {"msgtype":"text","text":{"content":...}}（键名与嵌套均与飞书不同），且 errcode=0 视为成功。
func TestSendTextDingTalkPayload(t *testing.T) {
	var gotBody map[string]any
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &gotBody); err != nil {
			t.Errorf("payload 非合法 JSON: %v", err)
		}
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	const dingURL = "http://oapi.dingtalk.com/robot/send?access_token=deadbeef"
	if err := send(dingURL, "钉钉测试", loopbackClient(t, srv.URL)); err != nil {
		t.Fatalf("钉钉 errcode=0 应成功，实际报错: %v", err)
	}
	if gotHost != "oapi.dingtalk.com" {
		t.Errorf("Host 头 = %q, want oapi.dingtalk.com", gotHost)
	}
	if _, exists := gotBody["msg_type"]; exists {
		t.Errorf("钉钉报文不应出现飞书键名 msg_type: %v", gotBody)
	}
	if gotBody["msgtype"] != "text" {
		t.Errorf("msgtype = %v, want text", gotBody["msgtype"])
	}
	text, ok := gotBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("text 结构不符: %v", gotBody["text"])
	}
	if text["content"] != "钉钉测试" {
		t.Errorf("text.content = %v, want 原文透传", text["content"])
	}
}

// TestSendTextDingTalkErrcode 钉钉语义：HTTP 200 但 body errcode!=0 应报错（token
// 非法/签名不匹配等场景钉钉就是这么回的）。
func TestSendTextDingTalkErrcode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"sign not match"}`))
	}))
	defer srv.Close()

	const dingURL = "http://oapi.dingtalk.com/robot/send?access_token=bad"
	err := send(dingURL, "hi", loopbackClient(t, srv.URL))
	if err == nil {
		t.Fatal("errcode=310000 应报错")
	}
	if !strings.Contains(err.Error(), "310000") {
		t.Errorf("错误应包含钉钉错误码，实际: %v", err)
	}
}

// TestSendTextDingTalkAckUnparseable 钉钉 200 但 body 解析不出 errcode（网关/代理的
// 非标准 200 响应）当成功，避免误报。
func TestSendTextDingTalkAckUnparseable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("gateway ok"))
	}))
	defer srv.Close()

	const dingURL = "http://oapi.dingtalk.com/robot/send?access_token=deadbeef"
	if err := send(dingURL, "hi", loopbackClient(t, srv.URL)); err != nil {
		t.Fatalf("body 无 errcode 应当成功，实际报错: %v", err)
	}
}

// TestSendTextErrorRedactsToken 网络错误文案不得泄漏完整 URL（内嵌机器人 token）：
// *url.Error 的 Error() 含完整 URL，send 重包装后只应保留 scheme://host。
func TestSendTextErrorRedactsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL + "/robot/send?access_token=supersecrettoken123"
	srv.Close() // 先起真实端口再关掉，保证地址必然连接拒绝

	err := SendText(url, "hi")
	if err == nil {
		t.Fatal("不可达地址应报错")
	}
	if strings.Contains(err.Error(), "supersecrettoken123") {
		t.Errorf("错误文案泄漏了 token: %v", err)
	}
	if !strings.Contains(err.Error(), "通知推送请求失败") {
		t.Errorf("错误文案应保留中文前缀，实际: %v", err)
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

// TestMarshalTextPayload 分派纯函数：按域名判定 + 报文键名（不经 HTTP 的直测）。
func TestMarshalTextPayload(t *testing.T) {
	t.Run("域名判定", func(t *testing.T) {
		cases := []struct {
			url  string
			want bool
		}{
			{"https://oapi.dingtalk.com/robot/send?access_token=x", true},
			{"https://oapi.dingtalk.com:443/robot/send", true},
			{"https://open.feishu.cn/open-apis/bot/v2/hook/xxx", false},
			{"http://127.0.0.1:9000/hook", false},
			{"://bad", false},
		}
		for _, tc := range cases {
			if got := isDingTalkURL(tc.url); got != tc.want {
				t.Errorf("isDingTalkURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		}
	})

	t.Run("钉钉报文键名", func(t *testing.T) {
		data, err := marshalTextPayload(true, "hi")
		if err != nil {
			t.Fatalf("序列化失败: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("非合法 JSON: %v", err)
		}
		if m["msgtype"] != "text" {
			t.Errorf("msgtype = %v, want text", m["msgtype"])
		}
		text, ok := m["text"].(map[string]any)
		if !ok || text["content"] != "hi" {
			t.Errorf("text.content 结构不符: %v", m)
		}
	})

	t.Run("飞书报文键名", func(t *testing.T) {
		data, err := marshalTextPayload(false, "hi")
		if err != nil {
			t.Fatalf("序列化失败: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("非合法 JSON: %v", err)
		}
		if m["msg_type"] != "text" {
			t.Errorf("msg_type = %v, want text", m["msg_type"])
		}
		content, ok := m["content"].(map[string]any)
		if !ok || content["text"] != "hi" {
			t.Errorf("content.text 结构不符: %v", m)
		}
	})
}
