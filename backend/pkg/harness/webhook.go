package harness

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// =============================================================================
// webhook 接收端（M14-T05，D6 定案：无状态验签入口）。
//
// 职责边界（诚实声明）：本文件只交付「验签 + 解析 + 派发」三件套——
// 不落库、不建 run 追踪表、不新立进程。NT harness 是无状态客户端，
// run 状态消费方按业务注入 consume 回调（提交方记账处本就持有 run_id，
// 无需本侧再造一份追踪面）；轮询 GetRun 仍是常态兜底，webhook 只做加速。
// 进程内挂载（挂到哪个 HTTP 服务）是 T04 实机对拍同一部署窗口的事。
//
// 跨仓契约对齐声明：SignV1/VerifyV1 与底座 pkg/outboundwebhook.SignBody、
// HCF apps/server/.../biz/agt/webhook_usecase.go signWebhookV1 逐字节同构——
// 消息形状 "<ts-unix>\n<body>"，签名头 "v1=<hex-hmac-sha256>"，重放窗 ±5min。
// =============================================================================

// WebhookMaxSkew 重放窗（±5min，底座建议值，HCF 接收端同构）。
const WebhookMaxSkew = 5 * time.Minute

// webhookBodyLimit 事件体上限（2MiB，HCF 接收端同构）。
const webhookBodyLimit = 2 << 20

// 事件类型词表（底座 twinEventEnvelope.event_type 实装词表）。
const (
	WebhookEventRunStarted       = "run.started"
	WebhookEventRunNodeStarted   = "run.node_started"
	WebhookEventRunNodeCompleted = "run.node_completed"
	WebhookEventRunWaiting       = "run.waiting_approval"
	WebhookEventRunNodeOutput    = "run.node_output"
	WebhookEventRunCompleted     = "run.completed"
	WebhookEventRunFailed        = "run.failed"
	WebhookEventRunCancelled     = "run.cancelled"
)

// WebhookEvent 底座 twinEventEnvelope 忠实投影（Payload 原文保留，
// 各事件类型形状：run.started {trace_url,total_nodes}；
// node_* {node_id,node_name,node_type,...}；run.waiting_approval
// {node_id,interrupt_id,node_name}；run.completed {output,duration_ms}；
// run.failed {error_message}；run.cancelled {}）。
type WebhookEvent struct {
	EventID    string         `json:"event_id"`
	EventType  string         `json:"event_type"`
	OccurredAt string         `json:"occurred_at"` // RFC3339 UTC（原文保留）
	RunID      string         `json:"run_id"`
	GraphID    string         `json:"graph_id"`
	Payload    map[string]any `json:"payload"`
}

// InterruptID waiting_approval 事件的审批中断标识（payload.interrupt_id，
// 底座保证 interrupt_id==node_id）。审批回路消费方把它连同 RunID 一并交给
// Client.ResumeRun；其他事件类型返回空串。
func (e *WebhookEvent) InterruptID() string {
	if e == nil || e.EventType != WebhookEventRunWaiting || e.Payload == nil {
		return ""
	}
	v, _ := e.Payload["interrupt_id"].(string)
	return v
}

// SignV1 计算签名头值：与底座 outboundwebhook.SignBody 逐字节同构
// （消息形状 "<ts>\n<body>"，前缀 "v1="）。投递侧与测试用；接收端走 VerifyV1。
func SignV1(secret, tsHeader string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(tsHeader + "\n"))
	_, _ = mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyV1 验签：secret 空 → ts 解析 → 时间窗 → 常量时间比对。失败四态合一
// ErrWebhookUnauthorized（绝不区分缺 secret/畸形/失配/越窗——防枚举）；
// Detail 不带任何密钥材料。now 注入便于测试。
func VerifyV1(secret, tsHeader, sigHeader string, body []byte, now time.Time) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return &Error{Kind: ErrWebhookUnauthorized, Detail: "webhook secret 未配置"}
	}
	tsHeader = strings.TrimSpace(tsHeader)
	ts, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return &Error{Kind: ErrWebhookUnauthorized, Detail: "X-Webhook-Timestamp 畸形"}
	}
	sent := time.Unix(ts, 0)
	if d := now.Sub(sent); d > WebhookMaxSkew || d < -WebhookMaxSkew {
		return &Error{Kind: ErrWebhookUnauthorized, Detail: "时间戳越出重放窗"}
	}
	if !hmac.Equal([]byte(SignV1(secret, tsHeader, body)), []byte(strings.TrimSpace(sigHeader))) {
		return &Error{Kind: ErrWebhookUnauthorized, Detail: "签名失配"}
	}
	return nil
}

// knownWebhookEvent 词表内事件类型（词表外=契约漂移，如实 400 不猜）。
func knownWebhookEvent(t string) bool {
	switch t {
	case WebhookEventRunStarted, WebhookEventRunNodeStarted, WebhookEventRunNodeCompleted,
		WebhookEventRunWaiting, WebhookEventRunNodeOutput,
		WebhookEventRunCompleted, WebhookEventRunFailed, WebhookEventRunCancelled:
		return true
	}
	return false
}

// WebhookHandler 无状态验签入口（http.Handler，挂到哪个 HTTP 服务由
// 消费方决定——本包不预设进程）。
type WebhookHandler struct {
	resolveSecret func(context.Context) (string, error) // 每请求现取（改密即时生效）
	consume       func(context.Context, *WebhookEvent) error
	now           func() time.Time
}

// NewWebhookHandler 组装入口。resolveSecret 每请求调用（密钥纪律：材料
// 按 ref 现取，严禁缓存进本结构体）；consume 收到的是验签+词表校验后的
// 事件，可为 nil（纯探测部署）。
func NewWebhookHandler(
	resolveSecret func(context.Context) (string, error),
	consume func(context.Context, *WebhookEvent) error,
) *WebhookHandler {
	return &WebhookHandler{resolveSecret: resolveSecret, consume: consume, now: time.Now}
}

// ServeHTTP POST-only / 体限 / 验签（401 同面，绝不区分失败原因）/
// 信封解析（400 契约漂移）/ 派发（500 上抛）。成功 200 {"ok":true}。
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		webhookReply(w, http.StatusMethodNotAllowed, `{"error":"method not allowed"}`)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, webhookBodyLimit+1))
	if err != nil || len(body) > webhookBodyLimit {
		webhookReply(w, http.StatusRequestEntityTooLarge, `{"error":"body too large"}`)
		return
	}
	if h.resolveSecret == nil {
		webhookReply(w, http.StatusUnauthorized, `{"error":"unauthorized"}`)
		return
	}
	secret, serr := h.resolveSecret(r.Context())
	if serr != nil {
		webhookReply(w, http.StatusUnauthorized, `{"error":"unauthorized"}`)
		return
	}
	if err := VerifyV1(secret,
		r.Header.Get("X-Webhook-Timestamp"),
		r.Header.Get("X-Webhook-Signature"),
		body, h.now()); err != nil {
		if errors.Is(err, ErrWebhookUnauthorized) {
			webhookReply(w, http.StatusUnauthorized, `{"error":"unauthorized"}`)
			return
		}
		webhookReply(w, http.StatusBadRequest, `{"error":"bad request"}`)
		return
	}
	var evt WebhookEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		webhookReply(w, http.StatusBadRequest, `{"error":"bad request"}`)
		return
	}
	if evt.EventID == "" || evt.RunID == "" || !knownWebhookEvent(evt.EventType) {
		webhookReply(w, http.StatusBadRequest, `{"error":"bad request"}`)
		return
	}
	if h.consume != nil {
		if err := h.consume(r.Context(), &evt); err != nil {
			webhookReply(w, http.StatusInternalServerError, `{"error":"internal"}`)
			return
		}
	}
	webhookReply(w, http.StatusOK, `{"ok":true}`)
}

func webhookReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}
