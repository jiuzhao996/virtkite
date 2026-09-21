package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/notify"
	"gorm.io/gorm"
)

// webhook 请求体上限：Alertmanager 单次推送（分组后）通常几十 KB，1MB 已是极宽上限
const webhookBodyLimit = 1 << 20

// AlertWebhookHandler 告警网关：接收 Alertmanager webhook 推送，按 fingerprint
// 去重入库（firing/resolved 覆盖更新），供 GET /api/monitor/alerts/history 追溯。
type AlertWebhookHandler struct {
	DB *gorm.DB
	// Token 鉴权令牌（config.ALERT_WEBHOOK_TOKEN）；为空表示公开接收
	Token string
}

// NewAlertWebhookHandler 创建告警网关处理器。
func NewAlertWebhookHandler(db *gorm.DB, token string) *AlertWebhookHandler {
	return &AlertWebhookHandler{DB: db, Token: token}
}

// AlertNotifyURLResolver 告警出站通知 webhook 地址的解析钩子，main 启动时接线到
// setting 服务（alert_notify_url 键，空=关闭）。参照 tasks.DefaultStoragePoolResolver
// 模式：本包不直接依赖 setting.Manager 实例，未接线（nil）或返回空串一律不通知。
var AlertNotifyURLResolver func() string

// amWebhookPayload Alertmanager webhook v4 payload（https://prometheus.io/docs/alerting/latest/configuration/#webhook_config）。
// version/groupKey/truncatedAlerts 等字段当前用不到，仅保留 status 便于日志排查。
type amWebhookPayload struct {
	Version  string    `json:"version"`
	GroupKey string    `json:"groupKey"`
	Status   string    `json:"status"`
	Alerts   []amAlert `json:"alerts"`
}

// amAlert webhook payload 中的单条告警
type amAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      *time.Time        `json:"endsAt"` // 指针：firing 告警不携带，避免零日期入库报错
	Fingerprint string            `json:"fingerprint"`
}

// Handle POST /api/monitor/webhook → 接收 Alertmanager 推送。
//
// 响应语义（重要约定）：除鉴权失败与请求体非法外一律 200——Alertmanager 对非 2xx
// 响应会按重试策略反复推送，处理失败（如 DB 抖动）时返回 200 只记日志，让 AM 侧
// repeat_interval 自然覆盖即可，避免重试轰炸。鉴权失败 401 属于配置错误，值得让
// 管理员立刻在 AM 侧看到推送失败，不按 200 吞掉。
func (h *AlertWebhookHandler) Handle(c *gin.Context) {
	if !h.checkToken(c) {
		log.Printf("[alert-webhook] webhook 鉴权失败，已拒绝（配置 ALERT_WEBHOOK_TOKEN 后需在 deploy/alertmanager.yml 同步携带）")
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, webhookBodyLimit))
	if err != nil {
		log.Printf("[alert-webhook] 读取请求体失败: %v", err)
		Fail(c, http.StatusBadRequest, "请求体读取失败")
		return
	}
	var payload amWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("[alert-webhook] payload JSON 解析失败: %v", err)
		Fail(c, http.StatusBadRequest, "payload 格式非法")
		return
	}

	alerts := convertAlerts(payload.Alerts)
	for _, a := range alerts {
		h.upsert(a) // 失败只记日志，不影响整体 200
	}
	Created(c, "ok", gin.H{"received": len(payload.Alerts), "stored": len(alerts)})
}

// checkToken 令牌校验：Token 为空=公开；非空时要求 ?token= 或 Authorization: Bearer 匹配。
func (h *AlertWebhookHandler) checkToken(c *gin.Context) bool {
	if h.Token == "" {
		return true
	}
	if c.Query("token") == h.Token {
		return true
	}
	return c.GetHeader("Authorization") == "Bearer "+h.Token
}

// convertAlerts payload → 入库模型（纯函数，可单测）。fingerprint 为空的告警跳过：
// 没有去重主键，重复推送会无限堆积。labels/annotations 序列化为 JSON 文本存储。
func convertAlerts(alerts []amAlert) []*model.Alert {
	out := make([]*model.Alert, 0, len(alerts))
	for _, a := range alerts {
		if a.Fingerprint == "" {
			continue
		}
		status := a.Status
		if status != model.AlertStatusResolved {
			// Alertmanager 只会发 firing/resolved；其余异常值一律归一为 firing，避免脏状态入库
			status = model.AlertStatusFiring
		}
		out = append(out, &model.Alert{
			Fingerprint: a.Fingerprint,
			Labels:      mustJSON(a.Labels),
			Annotations: mustJSON(a.Annotations),
			Status:      status,
			StartsAt:    a.StartsAt,
			EndsAt:      a.EndsAt,
		})
	}
	return out
}

// mustJSON map 序列化为 JSON 文本；nil map 序列化为 "{}" 而非 "null"（前端好按对象消费）。
func mustJSON(m map[string]string) string {
	if m == nil {
		m = map[string]string{}
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// upsert 按 fingerprint 去重入库：存在则更新 status/annotations/endsAt（firing → resolved
// 的终态流转全靠这里），不存在则新建。任何失败只记日志——调用方保证整体仍返回 200。
// 入库成功且构成「告警触发」（新建即 firing，或从非 firing 转回 firing）时，异步推送
// 出站通知（best-effort，见 pushAlertNotify）。
func (h *AlertWebhookHandler) upsert(a *model.Alert) {
	var existing model.Alert
	err := h.DB.Where("fingerprint = ?", a.Fingerprint).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := h.DB.Create(a).Error; err != nil {
				log.Printf("[alert-webhook] 写入告警失败 fingerprint=%s: %v", a.Fingerprint, err)
				return
			}
			if alertTransitionedToFiring(nil, a) {
				h.pushAlertNotify(a)
			}
			return
		}
		log.Printf("[alert-webhook] 查询告警失败 fingerprint=%s: %v", a.Fingerprint, err)
		return
	}
	updates := map[string]interface{}{
		"labels":      a.Labels,
		"annotations": a.Annotations,
		"status":      a.Status,
		"starts_at":   a.StartsAt,
		"ends_at":     a.EndsAt,
	}
	if err := h.DB.Model(&existing).Updates(updates).Error; err != nil {
		log.Printf("[alert-webhook] 更新告警失败 fingerprint=%s: %v", a.Fingerprint, err)
		return
	}
	// existing 是更新前读出的行：其 status 与本次 a.status 的对比即「是否转 firing」
	if alertTransitionedToFiring(&existing, a) {
		h.pushAlertNotify(a)
	}
}

// alertTransitionedToFiring 判断本次 upsert 后是否构成「告警触发」通知时机（纯函数）：
// 仅新建且 firing（existing == nil），或既有行从非 firing 状态转为 firing 时为 true。
// 去重考量：Alertmanager 按 group_interval/repeat_interval 会对同一 fingerprint 的
// firing 告警反复推送，此时库中已是 firing，不重复通知；resolved 一律不通知
// （恢复无需即时人工介入，且 resolved 推送量与告警抖动成正比，容易刷屏）。
func alertTransitionedToFiring(existing, incoming *model.Alert) bool {
	if incoming.Status != model.AlertStatusFiring {
		return false
	}
	if existing == nil {
		return true // 新建即 firing = 首次触发
	}
	return existing.Status != model.AlertStatusFiring
}

// notifySummaryLimit 通知文本中摘要部分的最大字符数（annotations 由告警规则作者
// 自由填写，防超长文本撑爆机器人消息）。
const notifySummaryLimit = 200

// alertNotifyText 组装告警触发通知文本（纯函数）：告警名取 labels.alertname，
// 摘要优先 annotations.summary、退化 annotations.description，均无则留空。
// labels/annotations 是入库前的 JSON 文本，解析失败按空处理（best-effort）。
func alertNotifyText(labelsJSON, annotationsJSON string) string {
	labels := map[string]string{}
	annotations := map[string]string{}
	_ = json.Unmarshal([]byte(labelsJSON), &labels)
	_ = json.Unmarshal([]byte(annotationsJSON), &annotations)
	summary := annotations["summary"]
	if summary == "" {
		summary = annotations["description"]
	}
	if utf8.RuneCountInString(summary) > notifySummaryLimit {
		rs := []rune(summary)
		summary = string(rs[:notifySummaryLimit-1]) + "…"
	}
	name := labels["alertname"]
	if name == "" {
		name = "未知告警"
	}
	return "[鸢航 VirtKite] 告警触发: " + name + " " + summary
}

// pushAlertNotify 异步推送告警触发通知（goroutine + defer recover——gin Recovery
// 不覆盖自起 goroutine，AGENTS 后端铁律第 10 条）。best-effort：失败只记日志，
// 绝不影响 webhook 的 200 响应（AM 对非 2xx 会按重试策略轰炸）。
// 日志不打印通知 URL（可能内嵌机器人 token 等凭据）。
func (h *AlertWebhookHandler) pushAlertNotify(a *model.Alert) {
	if AlertNotifyURLResolver == nil {
		return
	}
	url := AlertNotifyURLResolver()
	if url == "" {
		return // 未配置=通知关闭
	}
	text := alertNotifyText(a.Labels, a.Annotations)
	fingerprint := a.Fingerprint
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[alert-webhook] 告警通知协程 panic fingerprint=%s: %v\n%s", fingerprint, r, debug.Stack())
			}
		}()
		if err := notify.SendText(url, text); err != nil {
			log.Printf("[alert-webhook] 告警通知推送失败 fingerprint=%s: %v", fingerprint, err)
			return
		}
		log.Printf("[alert-webhook] 告警通知已推送 fingerprint=%s", fingerprint)
	}()
}
