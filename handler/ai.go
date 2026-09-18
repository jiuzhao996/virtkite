package handler

// v3 批次 B：AI 运维助手。
// OpenAI 兼容 /chat/completions 代理：API Key 只存服务端（system_settings），永不下发前端；
// with_context=true 时自动注入平台环境摘要（VM/容器/告警），使助手「看得懂这台服务器」。
// 流式响应（SSE）逐段转发；安全边界：AI 只读问答，不具备任何写操作能力（答辩固定话术）。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/setting"
	"gorm.io/gorm"
)

// AIHandler AI 运维助手。
type AIHandler struct {
	DB         *gorm.DB
	SettingMgr *setting.Manager
	Docker     *dockerx.Dockerx
}

// NewAIHandler 创建 AIHandler。
func NewAIHandler(db *gorm.DB, settingMgr *setting.Manager) *AIHandler {
	return &AIHandler{DB: db, SettingMgr: settingMgr, Docker: dockerx.New()}
}

// aiConfig 读取 AI 三项配置；未配置返回 ok=false。
func (h *AIHandler) aiConfig() (baseURL, apiKey, model string, ok bool) {
	baseURL = strings.TrimRight(h.SettingMgr.GetStr(setting.KeyAIBaseURL, ""), "/")
	apiKey = h.SettingMgr.GetStr(setting.KeyAIAPIKey, "")
	model = h.SettingMgr.GetStr(setting.KeyAIModel, "")
	ok = baseURL != "" && apiKey != "" && model != ""
	return
}

// Status GET /api/ai/status：配置状态（Key 掩码，不下发明文）。
func (h *AIHandler) Status(c *gin.Context) {
	baseURL, apiKey, model, ok := h.aiConfig()
	masked := ""
	if ok && len(apiKey) > 8 {
		masked = apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
	}
	Success(c, gin.H{
		"configured": ok,
		"model":      model,
		"base_url":   baseURL,
		"key_masked": masked,
	})
}

// platformSummary 平台环境摘要（注入 system prompt）。
func (h *AIHandler) platformSummary() string {
	var b strings.Builder
	var total, running int64
	h.DB.Model(&model.VM{}).Count(&total)
	h.DB.Model(&model.VM{}).Where("status = ?", model.VMStatusRunning).Count(&running)
	var pools, images int64
	h.DB.Model(&model.Image{}).Count(&images)
	b.WriteString("当前平台状态：")
	b.WriteString(fmt.Sprintf("虚拟机共 %d 台（运行中 %d）", total, running))
	if h.Docker != nil {
		if cs, err := h.Docker.Containers(); err == nil {
			rb := 0
			for _, c := range cs {
				if c.State == "running" {
					rb++
				}
			}
			b.WriteString(fmt.Sprintf("；Docker 容器 %d 个（运行中 %d）", len(cs), rb))
		}
	}
	b.WriteString(fmt.Sprintf("；镜像库 %d 条；存储池与网络以平台页面为准", pools+images))
	// 运行中 VM 清单（最多 20 台）
	var vms []model.VM
	h.DB.Where("status = ?", model.VMStatusRunning).Limit(20).Find(&vms)
	if len(vms) > 0 {
		names := make([]string, 0, len(vms))
		for _, v := range vms {
			names = append(names, fmt.Sprintf("%s(%d核/%dMB)", v.Name, v.VCPU, v.MemoryMB))
		}
		b.WriteString("。运行中：" + strings.Join(names, "、"))
	}
	// firing 告警数
	var firing int64
	h.DB.Model(&model.Alert{}).Where("status = ?", model.AlertStatusFiring).Count(&firing)
	b.WriteString(fmt.Sprintf("。当前 firing 告警 %d 条", firing))
	return b.String()
}

// chatMessage 上游消息结构。
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat POST /api/ai/chat  body: {messages:[{role,content}], with_context?:bool}
// SSE 流式转发上游 delta（OpenAI 兼容 chat/completions 格式）。
func (h *AIHandler) Chat(c *gin.Context) {
	baseURL, apiKey, model, ok := h.aiConfig()
	if !ok {
		Fail(c, http.StatusBadRequest, "AI 尚未配置：请由管理员在「系统设置 → AI 设置」填写 API 地址、Key 与模型名")
		return
	}
	var req struct {
		Messages    []chatMessage `json:"messages" binding:"required"`
		WithContext bool          `json:"with_context"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Messages) == 0 {
		Fail(c, http.StatusBadRequest, "参数错误（messages 不能为空）")
		return
	}
	// 消息长度保护：总内容 ≤ 64KB，条数 ≤ 40
	total := 0
	for _, m := range req.Messages {
		total += len(m.Content)
	}
	if total > 64*1024 || len(req.Messages) > 40 {
		Fail(c, http.StatusBadRequest, "会话内容过长，请清空会话后重试")
		return
	}

	sys := "你是鸢航 VirtKite 私有云管理平台的内置 AI 运维助手，帮助用户解答 KVM 虚拟化、Docker 容器、Linux 运维与平台使用问题。" +
		"回答使用简体中文，简洁准确；涉及删除/关机等危险操作时主动提醒确认。"
	if req.WithContext {
		sys += "\n以下是平台实时状态（供回答参考）：" + h.platformSummary()
	}

	msgs := make([]chatMessage, 0, len(req.Messages)+1)
	msgs = append(msgs, chatMessage{Role: "system", Content: sys})
	msgs = append(msgs, req.Messages...)

	payload := fmt.Sprintf(`{"model":%q,"messages":[`, model)
	for i, m := range msgs {
		if i > 0 {
			payload += ","
		}
		payload += fmt.Sprintf(`{"role":%q,"content":%s}`, m.Role, jsonString(m.Content))
	}
	payload += `],"stream":true}`

	upstream := baseURL + "/chat/completions"
	req2, err := http.NewRequest("POST", upstream, strings.NewReader(payload))
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "构造 AI 请求失败", err)
		return
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req2)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadGateway, "连接 AI 服务失败（请检查 API 地址与网络）", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body := make([]byte, 512)
		n, _ := resp.Body.Read(body)
		LogError(c, fmt.Errorf("AI 上游 %d: %s", resp.StatusCode, string(body[:n])))
		Fail(c, http.StatusBadGateway, "AI 服务返回异常（状态 "+fmt.Sprint(resp.StatusCode)+"），请检查 Key 与模型名")
		return
	}

	// SSE 透传
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	flusher, canFlush := c.Writer.(http.Flusher)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// 上游错误负载（非 SSE 格式的 JSON 错误）透出为事件
		if strings.HasPrefix(line, "data:") || strings.HasPrefix(line, "{") {
			if _, werr := c.Writer.WriteString(line + "\n\n"); werr != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

// jsonString JSON 字符串编码（含引号与转义）。
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
