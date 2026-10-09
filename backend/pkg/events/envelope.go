// Package events 是 NATS 上的事件信封和主题名。
// 订单、信号、风控、告警走 JetStream；market.* 走普通 NATS，不落盘。
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	StreamName = "NOVATRADER"

	SubjectSessionChanged = "sys.session.changed"
	SubjectKillSwitch     = "sys.killswitch"
	SubjectTradeOrder     = "trade.order"
	SubjectTradeFill      = "trade.fill"
	SubjectSignal         = "strategy.signal.updated" // 信号新建与每次状态变化，载荷见 M06 设计文档第 12 节
	SubjectRiskAlert      = "risk.alert"
	// M08 风控，载荷见 M08 设计文档第 6 节。sys.killswitch 只有 risk 发；其他服务要停机发 request。
	SubjectKillSwitchRequest = "sys.killswitch.request"
	SubjectRiskExit          = "risk.exit"
	SubjectTradeAccount      = "trade.account"
	SubjectMarketSentiment   = "market.sentiment.updated"
	SubjectNotifyRequest     = "notify.request"
	SubjectNotifyDesktop     = "notify.desktop"  // M10 发给 admin，再经 WebSocket 到桌面
	SubjectStrategyReview    = "strategy.review" // 盘后复盘。载荷 date、title、body
	SubjectMarketSnapshot    = "market.snapshot"
	SubjectMarketAlert       = "market.alert" // 个股异动，M06 用来触发盘中扫描：{"symbol","kind"}
	// M02 发布，载荷见 M02 设计文档第 8 节。
	SubjectMarketBar    = "market.bar.1m"
	SubjectMarketFactor = "market.factor.updated"
	SubjectMarketLimit  = "market.limit.changed"
	SubjectMarketState  = "market.state" // 情绪阶段、风险度、指数涨跌幅，给 M08

	// SubjectIntelRawPrefix 后接 kind：flash、news、announcement、report、social。
	SubjectIntelRawPrefix = "intel.raw."
	SubjectIntelRawAll    = "intel.raw.>"
	SubjectIntelScored    = "intel.news.scored"
	SubjectIntelAlert     = "intel.alert"

	// M01 发布，载荷见 M01 设计文档第 6.3 节。md.<域>.ready 为数据集采集完成。
	SubjectMDQuality = "md.quality"

	SubjectBrainProgress = "brain.progress" // 研判阶段进度，只给在线客户端看，载荷见 M05 设计文档第 11 节
	SubjectBriefing      = "strategy.briefing"
)

// Persistent 为真时主题进入 JetStream。行情快照和研判进度只走内存，不进流。
func Persistent(subject string) bool {
	return !strings.HasPrefix(subject, "market.") && subject != SubjectBrainProgress
}

// StreamSubjects 是 JetStream 流要捕获的主题。
func StreamSubjects() []string {
	return []string{"sys.>", "trade.>", "strategy.>", "risk.>", "notify.>", "intel.>", "md.>"}
}

// SubjectMDReady 是数据集采集完成的主题，例如 md.daily_bar.ready。
func SubjectMDReady(domain string) string { return "md." + domain + ".ready" }

// SubjectIntelRaw 是原始情报主题，kind 为 flash、news、announcement、report、social。
func SubjectIntelRaw(kind string) string { return SubjectIntelRawPrefix + kind }

type traceKey struct{}

// WithTraceID 把链路号放进 context，供发事件时填进信封。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceKey{}, traceID)
}

// TraceID 读取 context 里的链路号。没有时返回空字符串。
func TraceID(ctx context.Context) string {
	id, _ := ctx.Value(traceKey{}).(string)
	return id
}

// Envelope 是跨服务事件的固定外壳。消费端用 EventID 去重。
type Envelope struct {
	EventID string          `json:"event_id"`
	TraceID string          `json:"trace_id"`
	Time    time.Time       `json:"time"`
	Source  string          `json:"source"`
	Subject string          `json:"subject"`
	Payload json.RawMessage `json:"payload"`
}

// New 生成一条事件。event_id 由系统分配，调用方不能指定。
func New(source, subject, traceID string, payload any) (Envelope, error) {
	if strings.TrimSpace(source) == "" {
		return Envelope{}, fmt.Errorf("events: source is empty")
	}
	if strings.TrimSpace(subject) == "" || strings.ContainsAny(subject, " \t") {
		return Envelope{}, fmt.Errorf("events: invalid subject %q", subject)
	}
	raw, err := marshalPayload(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		EventID: uuid.NewString(),
		TraceID: traceID,
		Time:    time.Now().UTC(),
		Source:  source,
		Subject: subject,
		Payload: raw,
	}, nil
}

func marshalPayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return json.RawMessage(`{}`), nil
	}
	if raw, ok := payload.(json.RawMessage); ok {
		if len(raw) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return raw, nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("events: marshal payload: %w", err)
	}
	return b, nil
}

// Marshal 把信封编码成 JSON，供 NATS 发送。
func (e Envelope) Marshal() ([]byte, error) {
	if e.EventID == "" || e.Source == "" || e.Subject == "" {
		return nil, fmt.Errorf("events: envelope is incomplete")
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("events: marshal envelope: %w", err)
	}
	return b, nil
}

// Unmarshal 解析收到的消息。缺 event_id 时拒绝，避免无法去重的事件进入业务。
func Unmarshal(b []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return Envelope{}, fmt.Errorf("events: unmarshal: %w", err)
	}
	if env.EventID == "" || env.Source == "" || env.Subject == "" {
		return Envelope{}, fmt.Errorf("events: envelope is incomplete")
	}
	return env, nil
}
