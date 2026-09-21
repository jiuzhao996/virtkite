// Package notify 出站文本通知：向飞书/钉钉自定义机器人的 incoming webhook 推送
// {msg_type:"text", content:{text:...}} 文本消息（钉钉机器人加签名参数后兼容同一
// 结构的最小子集）。被 cron（计划任务失败通知）与 handler（告警触发通知）共用；
// 本包为叶子包，不 import 任何业务包，避免把消费方拖进导入环。
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// sendTimeout 推送超时：防通知端挂死拖住调用方（cron 调度协程 / 告警通知协程）。
const sendTimeout = 5 * time.Second

// textPayload 飞书/钉钉自定义机器人共用的最小 text 报文结构。
type textPayload struct {
	MsgType string `json:"msg_type"`
	Content struct {
		Text string `json:"text"`
	} `json:"content"`
}

// SendText 向 url 推送一条文本消息（等价 curl -X POST url -H 'Content-Type: application/json'
// -d '{"msg_type":"text","content":{"text":"..."}}'）。best-effort 语义由调用方保证：
// 失败返回错误只记日志，绝不影响主流程。非 2xx 状态码视为失败；网络错误以 %w 包装
// 中文错误返回，保留错误链。
func SendText(url, text string) error {
	var p textPayload
	p.MsgType = "text"
	p.Content.Text = text
	payload, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("通知报文序列化失败: %w", err)
	}
	client := &http.Client{Timeout: sendTimeout}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("通知推送请求失败: %w", err)
	}
	// 读掉响应体归还连接（内容不关心，只按状态码判定成败）
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("通知端返回状态码 %d", resp.StatusCode)
	}
	return nil
}
