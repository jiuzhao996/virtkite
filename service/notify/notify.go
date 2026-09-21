// Package notify 出站文本通知：向飞书/钉钉自定义机器人的 incoming webhook 推送
// 文本消息，按 URL 域名自动分派报文格式——host 含 oapi.dingtalk.com 发钉钉
// {"msgtype":"text","text":{"content":...}}，其余发飞书 {"msg_type":"text","content":{"text":...}}
// （两者键名与嵌套均不同，不可混用）。钉钉成功也是 HTTP 200，成败还要看 body 的 errcode。
// 被 cron（计划任务失败通知）与 handler（告警触发通知）共用；
// 本包为叶子包，不 import 任何业务包，避免把消费方拖进导入环。
package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// sendTimeout 推送超时：防通知端挂死拖住调用方（cron 调度协程 / 告警通知协程）。
const sendTimeout = 5 * time.Second

// dingtalkHostMark 钉钉机器人 webhook 的域名词根（https://oapi.dingtalk.com/robot/send?...）。
const dingtalkHostMark = "oapi.dingtalk.com"

// feishuPayload 飞书自定义机器人的最小 text 报文结构。
type feishuPayload struct {
	MsgType string `json:"msg_type"`
	Content struct {
		Text string `json:"text"`
	} `json:"content"`
}

// dingtalkPayload 钉钉自定义机器人的最小 text 报文结构（键名 msgtype、嵌套 text.content，
// 与飞书的 msg_type/content.text 完全不同——配钉钉地址发飞书报文钉钉会回参数错误）。
type dingtalkPayload struct {
	MsgType string `json:"msgtype"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
}

// SendText 向 url 推送一条文本消息（等价 curl -X POST url -H 'Content-Type: application/json'
// -d '<对应机器人的 text 报文>'）。按 URL 域名自动分派报文格式（见包注释）。
// best-effort 语义由调用方保证：失败返回错误只记日志，绝不影响主流程。
// 飞书按 HTTP 状态码判定成败（非 2xx 即失败）；钉钉非 2xx 同样失败，2xx 还要求
// body errcode==0。网络错误以 %w 包装中文错误返回保留错误链，文案不含完整 URL
// （机器人 webhook 地址内嵌 access_token 等凭据，落日志前已脱敏为 scheme://host）。
func SendText(rawURL, text string) error {
	return send(rawURL, text, nil)
}

// send 推送实现。client 参数供测试注入环回改写传输层——钉钉域名（oapi.dingtalk.com）
// 无法注册为 httptest 地址，借自定义 Transport 把「钉钉 URL」的连接改拨到环回测试
// 服务端，才能对真实发出的报文断言键名；生产路径传 nil，用默认 5s 超时 client。
func send(rawURL, text string, client *http.Client) error {
	dingtalk := isDingTalkURL(rawURL)
	payload, err := marshalTextPayload(dingtalk, text)
	if err != nil {
		return fmt.Errorf("通知报文序列化失败: %w", err)
	}
	if client == nil {
		client = &http.Client{Timeout: sendTimeout}
	}
	resp, err := client.Post(rawURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		// client.Post 的错误是 *url.Error，Error() 含完整 URL（内嵌机器人 token），
		// 直接落日志会泄漏凭据——重包装为只含 scheme://host 的文案，保留底层 %w 链
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("通知推送请求失败(%s): %w", redactURL(rawURL), ue.Err)
		}
		return fmt.Errorf("通知推送请求失败: %w", err)
	}
	// 读掉响应体归还连接（飞书不关心内容；钉钉要用 errcode 判成败）
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("通知端返回状态码 %d", resp.StatusCode)
	}
	if dingtalk {
		// 钉钉语义：HTTP 200 不代表成功，成败由 body 的 errcode 判定（0=成功）。
		// body 解析不出 errcode（网关/代理返回的非标准 200）当成功，避免误报。
		var ack struct {
			Errcode int `json:"errcode"`
		}
		if err := json.Unmarshal(body, &ack); err == nil && ack.Errcode != 0 {
			return fmt.Errorf("钉钉机器人返回错误码 %d", ack.Errcode)
		}
	}
	return nil
}

// isDingTalkURL 按 URL 域名判定是否钉钉机器人 webhook（host 含 oapi.dingtalk.com，
// 其余一律按飞书格式分派）。解析失败按飞书处理（后续请求自然报错）。
func isDingTalkURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.Contains(u.Host, dingtalkHostMark)
}

// marshalTextPayload 序列化对应机器人的最小 text 报文（纯函数，可单测）。
func marshalTextPayload(dingtalk bool, text string) ([]byte, error) {
	if dingtalk {
		var p dingtalkPayload
		p.MsgType = "text"
		p.Text.Content = text
		return json.Marshal(p)
	}
	var p feishuPayload
	p.MsgType = "text"
	p.Content.Text = text
	return json.Marshal(p)
}

// redactURL 把 webhook 地址脱敏为 scheme://host（钉钉/飞书 incoming 地址的 path/query
// 内嵌 access_token 等凭据，不能原样进错误文案与日志）。解析失败或无 host 视为
// 地址本身非法，返回占位文案。
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "(地址不可解析)"
	}
	return u.Scheme + "://" + u.Host
}
