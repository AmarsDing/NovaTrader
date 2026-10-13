package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"server/pkg/events"
	"server/utils/websocket"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/nats-io/nats.go"
)

const ringCap = 500

var allowedTopics = map[string]struct{}{
	"signal": {}, "position": {}, "alert": {}, "market": {}, "brain": {},
}

type frame struct {
	Topic string
	Event string
	Seq   int64
	Raw   json.RawMessage
}

// Hub 按主题转发 NATS，并按序号补推断线期间的消息。序号只活在本进程里。
type Hub struct {
	log    *log.Helper
	mu     sync.Mutex
	seq    map[string]int64
	buf    map[string][]frame
	market json.RawMessage
	bus    *events.Bus
}

func NewHub(logger log.Logger) *Hub {
	h := &Hub{
		log: log.NewHelper(log.With(logger, "module", "admin/hub")),
		seq: map[string]int64{},
		buf: map[string][]frame{},
	}
	websocket.TextHandler = h.onText
	return h
}

// Start 连接 NATS 并每秒刷一次行情。NATS 暂时连不上会重试，不挡住 admin。
func (h *Hub) Start(natsURL string) func() {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		h.flushLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		h.listen(ctx, natsURL)
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			h.closeBus()
			wg.Wait()
		})
	}
}

// Up 表示当前连着 NATS。
func (h *Hub) Up() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	bus := h.bus
	h.mu.Unlock()
	return bus != nil && bus.Conn() != nil && bus.Conn().IsConnected()
}

// Stats 返回 NATS 运行口径：连接计数、各订阅积压、durable 消费者 lag。
// 供 /api/ops/nats 使用；NATS 断连时返回 connected=false 而不是错误。
func (h *Hub) Stats() map[string]any {
	h.mu.Lock()
	bus := h.bus
	h.mu.Unlock()
	if bus == nil || bus.Conn() == nil || !bus.Conn().IsConnected() {
		return map[string]any{"connected": false}
	}
	nc := bus.Conn()
	stats := nc.Stats()
	out := map[string]any{
		"connected":  true,
		"in_msgs":    stats.InMsgs,
		"out_msgs":   stats.OutMsgs,
		"in_bytes":   stats.InBytes,
		"out_bytes":  stats.OutBytes,
		"reconnects": stats.Reconnects,
	}
	if bus.JetStream() != nil {
		if info, err := bus.JetStream().StreamInfo(events.StreamName); err == nil {
			stream := map[string]any{
				"name":         info.Config.Name,
				"messages":     info.State.Msgs,
				"bytes":        info.State.Bytes,
				"first_seq":    info.State.FirstSeq,
				"last_seq":     info.State.LastSeq,
				"num_subjects": info.State.NumSubjects,
			}
			consumers := []map[string]any{}
			for _, route := range natsRoutes {
				if route.core || route.durable == "" {
					continue
				}
				ci, err := bus.JetStream().ConsumerInfo(events.StreamName, route.durable)
				if err != nil {
					consumers = append(consumers, map[string]any{
						"durable": route.durable, "subject": route.subject, "missing": true,
					})
					continue
				}
				lag := int64(info.State.LastSeq) - int64(ci.Delivered.Stream)
				if lag < 0 {
					lag = 0
				}
				consumers = append(consumers, map[string]any{
					"durable":       route.durable,
					"subject":       route.subject,
					"pending":       ci.NumPending,
					"ack_pending":   ci.NumAckPending,
					"delivered_seq": ci.Delivered.Stream,
					"lag":           lag,
				})
			}
			stream["consumers"] = consumers
			out["stream"] = stream
		}
	}
	return out
}

// Publish 写入环形缓冲并推给已订阅的连接。行情先攒着，由 flush 每秒发一次。
func (h *Hub) Publish(topic, event string, payload any) {
	if h == nil || topic == "" {
		return
	}
	raw := asRaw(payload)
	if topic == "market" {
		h.mu.Lock()
		h.market = raw
		h.mu.Unlock()
		return
	}
	h.add(topic, event, raw)
}

func (h *Hub) onText(client *websocket.Client, message []byte) bool {
	var req struct {
		Op     string           `json:"op"`
		Topics []string         `json:"topics"`
		Resume map[string]int64 `json:"resume"`
	}
	if err := json.Unmarshal(message, &req); err != nil || req.Op == "" {
		return false
	}
	if req.Op != "sub" {
		return true
	}
	topics := make([]string, 0, len(req.Topics))
	for _, topic := range req.Topics {
		if _, ok := allowedTopics[topic]; ok {
			topics = append(topics, topic)
		}
	}
	for _, frame := range h.subscribe(client, topics, req.Resume) {
		trySend(client, frame.response())
	}
	trySend(client, &websocket.WResponse{Event: "sub", Data: "ok"})
	return true
}

func (h *Hub) subscribe(client *websocket.Client, topics []string, resume map[string]int64) []frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	client.SetTopics(topics)
	if resume == nil {
		return nil
	}
	var out []frame
	for _, topic := range topics {
		after, ok := resume[topic]
		if !ok {
			continue
		}
		for _, item := range h.buf[topic] {
			if item.Seq > after {
				out = append(out, item)
			}
		}
	}
	return out
}

func (h *Hub) add(topic, event string, raw json.RawMessage) {
	if event == "" {
		event = topic
	}
	h.mu.Lock()
	h.seq[topic]++
	item := frame{Topic: topic, Event: event, Seq: h.seq[topic], Raw: append(json.RawMessage(nil), raw...)}
	h.buf[topic] = append(h.buf[topic], item)
	if len(h.buf[topic]) > ringCap {
		h.buf[topic] = append([]frame(nil), h.buf[topic][len(h.buf[topic])-ringCap:]...)
	}
	h.mu.Unlock()
	msg := item.response()
	websocket.RangeClients(func(client *websocket.Client) {
		if client.Allows(topic) {
			trySend(client, msg)
		}
	})
}

func (h *Hub) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.flushMarket()
		}
	}
}

func (h *Hub) flushMarket() {
	h.mu.Lock()
	raw := h.market
	h.market = nil
	h.mu.Unlock()
	if len(raw) == 0 {
		return
	}
	h.add("market", "market", raw)
}

func (h *Hub) listen(ctx context.Context, natsURL string) {
	for {
		if ctx.Err() != nil {
			return
		}
		bus, err := events.Dial(natsURL)
		if err != nil {
			h.log.Warnf("桌面推送暂停：%v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}
		h.setBus(bus)
		h.bind(bus)
		<-ctx.Done()
		bus.Close()
		h.setBus(nil)
		return
	}
}

func (h *Hub) setBus(bus *events.Bus) {
	h.mu.Lock()
	h.bus = bus
	h.mu.Unlock()
}

func (h *Hub) closeBus() {
	h.mu.Lock()
	bus := h.bus
	h.bus = nil
	h.mu.Unlock()
	if bus != nil {
		bus.Close()
	}
}

type natsRoute struct {
	subject string
	topic   string
	event   string
	durable string
	core    bool
}

// natsRoutes 是桌面推送的订阅表，bind 和 Stats 共用（Stats 需要 durable 名单查积压）。
var natsRoutes = []natsRoute{
	{events.SubjectSignal, "signal", "signal", "admin_signal", false},
	{events.SubjectTradeAccount, "position", "position", "admin_trade_account", false},
	{events.SubjectTradeOrder, "position", "position", "admin_trade_order", false},
	{events.SubjectTradeFill, "position", "position", "admin_trade_fill", false},
	{events.SubjectRiskAlert, "alert", "alert", "admin_risk_alert", false},
	{events.SubjectKillSwitch, "alert", "alert", "admin_killswitch", false},
	{events.SubjectIntelAlert, "alert", "alert", "admin_intel_alert", false},
	{events.SubjectNotifyDesktop, "alert", "notify", "admin_notify_desktop", false},
	{events.SubjectStrategyReview, "alert", "alert", "admin_strategy_review", false},
	{events.SubjectBriefing, "alert", "alert", "admin_briefing", false},
	{events.SubjectMarketSnapshot, "market", "market", "", true},
	{events.SubjectBrainProgress, "brain", "brain", "", true},
}

func (h *Hub) bind(bus *events.Bus) {
	for _, item := range natsRoutes {
		item := item
		if item.core {
			if _, err := bus.Conn().Subscribe(item.subject, func(m *nats.Msg) {
				h.onRaw(item.topic, item.event, m.Data)
			}); err != nil {
				h.log.Warnf("订阅 %s：%v", item.subject, err)
			}
			continue
		}
		_, err := bus.JetStream().Subscribe(item.subject, func(m *nats.Msg) {
			h.onRaw(item.topic, item.event, m.Data)
			_ = m.Ack()
		}, nats.Durable(item.durable), nats.ManualAck(), nats.DeliverNew(),
			nats.AckWait(10*time.Second), nats.MaxDeliver(5))
		if err != nil {
			h.log.Warnf("订阅 %s：%v", item.subject, err)
		}
	}
	h.log.Info("桌面推送已订阅")
}

func (h *Hub) onRaw(topic, event string, body []byte) {
	env, err := events.Unmarshal(body)
	if err != nil {
		h.log.Warnf("坏事件 %s：%v", topic, err)
		return
	}
	h.Publish(topic, event, env.Payload)
}

func (f frame) response() *websocket.WResponse {
	payload := decodeRaw(f.Raw)
	event := f.Event
	if event == "" {
		event = f.Topic
	}
	return &websocket.WResponse{
		Event:   event,
		Data:    payload,
		Topic:   f.Topic,
		Seq:     f.Seq,
		Payload: payload,
	}
}

func trySend(client *websocket.Client, msg *websocket.WResponse) {
	if client == nil || client.SendClose || msg == nil {
		return
	}
	defer func() { _ = recover() }()
	select {
	case client.Send <- msg:
	default:
	}
}

func asRaw(payload any) json.RawMessage {
	switch v := payload.(type) {
	case nil:
		return json.RawMessage(`{}`)
	case json.RawMessage:
		if len(v) == 0 {
			return json.RawMessage(`{}`)
		}
		return v
	case []byte:
		if len(v) == 0 {
			return json.RawMessage(`{}`)
		}
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return json.RawMessage(`{}`)
		}
		return b
	}
}

func decodeRaw(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{}
	}
	return v
}
