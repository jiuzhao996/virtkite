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
//   - DB：读取 VM/镜像/告警摘要（with_context 用）
//   - SettingMgr：读取 AI 三项配置（base_url/api_key/model），Key 只存服务端
//   - Docker：容器计数，失败按「Docker 不可用」降级，不影响问答
type AIHandler struct {
	DB         *gorm.DB
	SettingMgr *setting.Manager
	Docker     *dockerx.Dockerx
}

// NewAIHandler 创建 AIHandler。Docker 在构造时初始化（懒连接），
// 即使宿主机没装 Docker 也不会阻塞 handler 创建。
func NewAIHandler(db *gorm.DB, settingMgr *setting.Manager) *AIHandler {
	return &AIHandler{DB: db, SettingMgr: settingMgr, Docker: dockerx.New()}
}

// aiConfig 读取 AI 三项配置；未配置返回 ok=false。
// 三项必须同时存在才算可用（缺任一都无法发起 chat/completions 请求）；
// base_url 去掉尾部 "/" 再拼接 "/chat/completions"，避免出现双斜杠。
func (h *AIHandler) aiConfig() (baseURL, apiKey, model string, ok bool) {
	baseURL = strings.TrimRight(h.SettingMgr.GetStr(setting.KeyAIBaseURL, ""), "/")
	apiKey = h.SettingMgr.GetStr(setting.KeyAIAPIKey, "")
	model = h.SettingMgr.GetStr(setting.KeyAIModel, "")
	ok = baseURL != "" && apiKey != "" && model != ""
	return
}

// Status GET /api/ai/status：配置状态（Key 掩码，不下发明文）。
// 掩码规则：仅当 Key 长度 > 8 时显示前 4 + **** + 后 4；过短的 Key 直接不回显，
// 避免掩码后反而暴露更多字符。前端只用 configured 判断是否展示聊天入口。
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
// 全部统计 best-effort：任一查询失败只表现为对应数字为 0，不影响问答主流程；
// 运行中 VM 最多列 20 台，避免 prompt 随规模无限膨胀（token 成本与上游限长）。
func (h *AIHandler) platformSummary() string {
	var b strings.Builder
	var total, running int64
	h.DB.Model(&model.VM{}).Count(&total)
	h.DB.Model(&model.VM{}).Where("status = ?", model.VMStatusRunning).Count(&running)
	var images int64
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
	b.WriteString(fmt.Sprintf("；镜像库 %d 条；存储池与网络以平台页面为准", images))
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

// chatMessage 上游消息结构（OpenAI chat/completions 的最小字段集）。
// 只保留 role/content：其余字段（name/tool_calls 等）平台不使用，也不透传，
// 避免客户端借消息字段向上游注入额外能力。
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat POST /api/ai/chat  body: {messages:[{role,content}], with_context?:bool}
// SSE 流式转发上游 delta（OpenAI 兼容 chat/completions 格式）。
// 安全边界：本接口只做只读问答代理，不解析/执行模型输出，也不提供任何工具调用，
// 因此 AI 无法直接操作平台（答辩口径：AI 只读问答）。
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
	// 消息长度保护：总内容 ≤ 64KB，条数 ≤ 40。
	// 这是成本/滥用闸而非安全边界：防止单请求把上游额度打满或撑爆内存。
	total := 0
	for _, m := range req.Messages {
		total += len(m.Content)
	}
	if total > 64*1024 || len(req.Messages) > 40 {
		Fail(c, http.StatusBadRequest, "会话内容过长，请清空会话后重试")
		return
	}

	// system prompt 由服务端固定，客户端无法覆盖：既统一回答风格，
	// 也防止用户把「忽略以上指令」之类内容塞进 system 角色。
	// with_context 时追加平台实时摘要，让助手能回答「现在有几台机器在跑」。
	sys := "你是鸢航 VirtKite 私有云管理平台的内置 AI 运维助手，帮助用户解答 KVM 虚拟化、Docker 容器、Linux 运维与平台使用问题。" +
		"回答使用简体中文，简洁准确；涉及删除/关机等危险操作时主动提醒确认。"
	if req.WithContext {
		sys += "\n以下是平台实时状态（供回答参考）：" + h.platformSummary()
	}

	// system 消息固定在首位，其后才是客户端历史消息（顺序即上游语义）
	msgs := make([]chatMessage, 0, len(req.Messages)+1)
	msgs = append(msgs, chatMessage{Role: "system", Content: sys})
	msgs = append(msgs, req.Messages...)

	// model 与 role 都在 message 里，同样经 jsonString 做 JSON 转义——
	// 原先用 Go 的 %q：它把控制字符转成 \a/\v 等**非 JSON 转义**，会让整个 payload 非法、上游 400。
	// 手写 payload 而非结构体序列化，是为了按上游格式精确控制字段与流式开关。
	payload := fmt.Sprintf(`{"model":%s,"messages":[`, jsonString(model))
	for i, m := range msgs {
		if i > 0 {
			payload += ","
		}
		payload += fmt.Sprintf(`{"role":%s,"content":%s}`, jsonString(m.Role), jsonString(m.Content))
	}
	payload += `],"stream":true}`

	upstream := baseURL + "/chat/completions"
	req2, err := http.NewRequest("POST", upstream, strings.NewReader(payload))
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "构造 AI 请求失败", err)
		return
	}
	req2.Header.Set("Content-Type", "application/json")
	// Key 只在此处作为 Bearer 发往用户配置的上游，绝不写日志/响应
	req2.Header.Set("Authorization", "Bearer "+apiKey)
	// 5 分钟上限：流式回答可能较长，但必须有硬超时，防止上游挂死占住连接
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req2)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadGateway, "连接 AI 服务失败（请检查 API 地址与网络）", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 只读前 512 字节进日志：上游错误体可能很长，且可能回显 Key 片段；
		// 前端只收状态码 + 中文排查提示（handler 边界不泄漏内部错误）
		body := make([]byte, 512)
		n, _ := resp.Body.Read(body)
		LogError(c, fmt.Errorf("AI 上游 %d: %s", resp.StatusCode, string(body[:n])))
		Fail(c, http.StatusBadGateway, "AI 服务返回异常（状态 "+fmt.Sprint(resp.StatusCode)+"），请检查 Key 与模型名")
		return
	}

	// SSE 透传：响应头必须在写第一段数据前设置，否则 Gin 已提交 200 + 默认类型。
	// X-Accel-Buffering: no 让 Nginx 不缓冲，保证逐字流式到达浏览器。
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	flusher, canFlush := c.Writer.(http.Flusher)
	scanner := bufio.NewScanner(resp.Body)
	// 单行上限 256KB：SSE 的 data 行可能包含较长 delta/错误 JSON，
	// 默认 64KB 会触发 ErrTooLong 并静默截断流。
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// 上游错误负载（非 SSE 格式的 JSON 错误）透出为事件。
		// 只转发 data: 行与 JSON 行，过滤注释/心跳等无关行，避免污染前端解析。
		if strings.HasPrefix(line, "data:") || strings.HasPrefix(line, "{") {
			if _, werr := c.Writer.WriteString(line + "\n\n"); werr != nil {
				// 客户端已断开：直接结束，不写错误（连接都没了也无处可写）
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

// jsonString JSON 字符串编码（含引号与转义）。
// 用 encoding/json 而不是 fmt %q：后者产生 Go 语法转义（\a、\v、\xNN），
// 放进 JSON 会导致上游解析失败。Marshal 对 string 不会失败，故忽略 error。
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
