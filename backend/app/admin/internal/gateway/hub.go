package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"server/pkg/events"
	"server/utils/websocket"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

const ringCap = 500

var allowedTopics = map[string]struct{}{
	"signal": {}, "position": {}, "alert": {}, "market": {}, "brain": {},
}

type frame struct {
	Topic    string
	Event    string
	Seq      int64
	Raw      json.RawMessage
	Epoch    string
	Metadata map[string]any
	// publish 时一次解析沉淀的行情身份（仅 market 主题），供按客户端兴趣过滤，免每帧每客户端重解析。
	Symbol string
	Freq   string
}

type topicCursor struct {
	Head   int64  `json:"head"`
	Oldest int64  `json:"oldest"`
	After  int64  `json:"after"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// marketFilter 是客户端声明的行情兴趣（P0-3 订阅收窄）；Symbol 必填，Freq 为空表示该证券全周期。
type marketFilter struct {
	Symbol string `json:"symbol"`
	Freq   string `json:"freq"`
}

type clientSubscription struct {
	topics    []string
	pending   map[string]int64 // REST recovery must acknowledge this frozen watermark.
	requestID uint64
	// 行情兴趣：marketAll 为真（未声明过滤）时全量；否则按 symbol[/freq] 键集合放行。
	marketAll     bool
	marketKeys    map[string]struct{}
	marketSymbols map[string]struct{}
}

// Hub keeps a bounded replay ring. A cursor is only valid within its server epoch.
type Hub struct {
	log             *log.Helper
	mu              sync.Mutex
	seq             map[string]int64
	buf             map[string][]frame
	market          map[string]frame
	epoch           string
	clients         map[*websocket.Client]*clientSubscription
	observations    *observationLedger
	slowDisconnects uint64
	resyncs         uint64
	bus             *events.Bus
}

func NewHub(logger log.Logger) *Hub {
	h := &Hub{
		log:     log.NewHelper(log.With(logger, "module", "admin/hub")),
		seq:     map[string]int64{},
		buf:     map[string][]frame{},
		market:  map[string]frame{},
		epoch:   uuid.NewString(),
		clients: map[*websocket.Client]*clientSubscription{},
	}
	h.observations = newObservationLedger(h.epoch, time.Now)
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
	protocol := map[string]any{"server_epoch": h.epoch, "slow_disconnects": h.slowDisconnects, "resyncs": h.resyncs, "replay_capacity": ringCap}
	h.mu.Unlock()
	if bus == nil || bus.Conn() == nil || !bus.Conn().IsConnected() {
		return map[string]any{"connected": false, "ws": protocol}
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
		"ws":         protocol,
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
				row := map[string]any{
					"durable":       route.durable,
					"subject":       route.subject,
					"pending":       ci.NumPending,
					"ack_pending":   ci.NumAckPending,
					"delivered_seq": ci.Delivered.Stream,
					"lag":           ci.NumPending,
					"lag_basis":     "filtered_consumer_num_pending",
					"redelivered":   ci.NumRedelivered,
				}
				// 持久积压最老年龄（N2-01）：两个计数都为零就不必再读流，-1 如实表示无/不可解。
				if ci.NumPending > 0 || ci.NumAckPending > 0 {
					ages := oldestBacklog(bus.JetStream(), events.StreamName, route.subject, ci, time.Now())
					row["oldest_pending_age_ms"] = ages.PendingAgeMs
					row["oldest_ack_age_ms"] = ages.AckAgeMs
					row["oldest_age_basis"] = ages.Basis
				} else {
					row["oldest_pending_age_ms"] = int64(-1)
					row["oldest_ack_age_ms"] = int64(-1)
					row["oldest_age_basis"] = "pending:无积压;ack:无未确认"
				}
				consumers = append(consumers, row)
			}
			stream["consumers"] = consumers
			out["stream"] = stream
		}
	}
	return out
}

// Publish is also used by local producers without an event envelope.
func (h *Hub) Publish(topic, event string, payload any) {
	h.publish(topic, event, asRaw(payload), nil)
}

func (h *Hub) publish(topic, event string, raw json.RawMessage, metadata map[string]any) {
	if h == nil || topic == "" {
		return
	}
	if _, ok := allowedTopics[topic]; !ok {
		return
	}
	if event == "" {
		event = topic
	}
	item := frame{Topic: topic, Event: event, Raw: append(json.RawMessage(nil), raw...), Metadata: metadata}
	if topic == "market" {
		key, symbol, freq := marketKey(item)
		item.Symbol, item.Freq = symbol, freq
		if key != "" {
			h.mu.Lock()
			_, exists := h.market[key]
			if exists || len(h.market) < 5000 {
				h.market[key] = item
				h.mu.Unlock()
				return
			}
			h.mu.Unlock()
		}
	}
	h.addFrame(item)
}

// Only replaceable market states may be coalesced. Unknown payloads remain events.
// 返回合并键与帧的证券身份；identity 在一次解析里顺手产出，供按客户端兴趣过滤。
func marketKey(item frame) (key, symbol, freq string) {
	var payload map[string]any
	_ = json.Unmarshal(item.Raw, &payload)
	for _, name := range []string{"symbol", "stock_code", "code"} {
		if s, ok := payload[name].(string); ok && s != "" {
			symbol = s
			break
		}
	}
	freq, _ = payload["freq"].(string)
	kind := item.Event
	if subject, ok := item.Metadata["subject"].(string); ok && subject != "" {
		kind = subject
	}
	// Market alerts and limit transitions are events, not replaceable quotations.
	switch kind {
	case "tick", "quote", "market", "bar", events.SubjectMarketSnapshot, events.SubjectMarketBar, events.SubjectMarketFactor:
	default:
		return "", symbol, freq
	}
	bucket := ""
	if strings.Contains(kind, "bar") {
		values := payload
		if nested, ok := payload["bar"].(map[string]any); ok {
			values = nested
		}
		for _, field := range []string{"time", "timestamp", "start_time", "end_time", "bar_time"} {
			if value, ok := values[field]; ok && value != nil {
				bucket = fmt.Sprint(value)
				break
			}
		}
		if bucket == "" {
			return "", symbol, freq
		} // Do not erase a completed bar without its time key.
	}
	if symbol != "" {
		return kind + ":" + symbol + ":" + freq + ":" + bucket, symbol, freq
	}
	if kind == events.SubjectMarketSnapshot {
		source, _ := item.Metadata["source"].(string)
		return kind + ":batch:" + source, "", ""
	}
	return "", symbol, freq
}

// allowsMarket 实现 P0-3 的服务端收窄：快照 meta/无证券身份的事件帧全量放行；
// freq 不可判定的帧按证券级放行（宁多勿丢，前端再按行内 freq 过滤）。
func (s *clientSubscription) allowsMarket(item frame) bool {
	if item.Topic != "market" || s.marketAll {
		return true
	}
	if item.Symbol == "" {
		return true
	}
	if item.Freq == "" {
		_, ok := s.marketSymbols[item.Symbol]
		return ok
	}
	if _, ok := s.marketKeys[item.Symbol+"\x00"+item.Freq]; ok {
		return true
	}
	_, ok := s.marketKeys[item.Symbol+"\x00"]
	return ok
}

func (h *Hub) onText(client *websocket.Client, message []byte) bool {
	var req struct {
		Op            string           `json:"op"`
		Topics        []string         `json:"topics"`
		Resume        map[string]int64 `json:"resume"`
		Epoch         string           `json:"server_epoch"`
		RequestID     uint64           `json:"request_id"`
		MarketFilters []marketFilter   `json:"market_filters"`
	}
	if err := json.Unmarshal(message, &req); err != nil || req.Op == "" {
		return false
	}
	switch req.Op {
	case "sub", "resync":
		h.beginSubscription(client, req.Topics, req.Resume, req.Epoch, req.RequestID, req.Op == "resync", req.MarketFilters)
	case "resync_ack":
		h.completeRecovery(client, req.Resume, req.Epoch, req.RequestID)
	}
	return true
}

func (h *Hub) cursor(topic string, after int64) topicCursor {
	oldest := h.seq[topic] + 1
	if items := h.buf[topic]; len(items) > 0 {
		oldest = items[0].Seq
	}
	return topicCursor{Head: h.seq[topic], Oldest: oldest, After: after, Status: "resumed"}
}

func (h *Hub) control(op string, requestID uint64, data any) *websocket.WResponse {
	return &websocket.WResponse{Event: op, Topic: op, Data: data, Payload: data, ServerEpoch: h.epoch, RequestID: requestID}
}

// The lock serializes ack, replay and live publication. No event can cross the handoff.
// filters 为客户端声明的行情兴趣（P0-3）；为空表示未声明即全量（向后兼容）。
func (h *Hub) beginSubscription(client *websocket.Client, topics []string, resume map[string]int64, epoch string, requestID uint64, force bool, filters []marketFilter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if client == nil {
		return
	}
	selected := []string{}
	seen := map[string]bool{}
	for _, topic := range topics {
		if _, allowed := allowedTopics[topic]; allowed && !seen[topic] {
			selected = append(selected, topic)
			seen[topic] = true
		}
	}
	state := &clientSubscription{topics: selected, pending: map[string]int64{}, requestID: requestID, marketAll: len(filters) == 0}
	if !state.marketAll {
		state.marketKeys = map[string]struct{}{}
		state.marketSymbols = map[string]struct{}{}
		for _, f := range filters {
			symbol := strings.TrimSpace(f.Symbol)
			if symbol == "" {
				continue
			}
			state.marketKeys[symbol+"\x00"+strings.TrimSpace(f.Freq)] = struct{}{}
			state.marketSymbols[symbol] = struct{}{}
		}
		// 全部条目都缺 symbol 视为未声明有效兴趣——回到全量，拒绝"过滤成空流"。
		if len(state.marketKeys) == 0 {
			state.marketAll = true
		}
	}
	h.clients[client] = state
	client.SetTopics(selected)
	cursors := map[string]topicCursor{}
	replays := []frame{}
	for _, topic := range selected {
		after, present := resume[topic]
		cursor := h.cursor(topic, after)
		switch {
		case epoch == "":
			cursor.Reason = "epoch_required"
		case epoch != h.epoch:
			cursor.Reason = "epoch_changed"
		case !present:
			cursor.Reason = "snapshot_required"
		case after < 0 || after > cursor.Head:
			cursor.Reason = "cursor_ahead"
		case after < cursor.Oldest-1:
			cursor.Reason = "replay_expired"
		case force:
			cursor.Reason = "client_resync"
		}
		if cursor.Reason != "" {
			cursor.Status = "resync_required"
			state.pending[topic] = cursor.Head
			h.resyncs++
		} else {
			for _, item := range h.buf[topic] {
				if item.Seq > after && state.allowsMarket(item) {
					replays = append(replays, item)
				}
			}
		}
		cursors[topic] = cursor
	}
	if !h.sendLocked(client, h.control("sub_ack", requestID, map[string]any{"topics": cursors})) {
		return
	}
	if len(state.pending) > 0 {
		required := map[string]topicCursor{}
		for topic := range state.pending {
			required[topic] = cursors[topic]
		}
		if !h.sendLocked(client, h.control("resync_required", requestID, map[string]any{"topics": required})) {
			return
		}
	}
	h.replayLocked(client, replays, requestID)
}

func (h *Hub) completeRecovery(client *websocket.Client, watermarks map[string]int64, epoch string, requestID uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, ok := h.clients[client]
	if !ok || epoch != h.epoch || state.requestID != requestID {
		return
	}
	cursors := map[string]topicCursor{}
	replays := []frame{}
	for topic, after := range watermarks {
		frozen, waiting := state.pending[topic]
		if !waiting {
			continue
		}
		cursor := h.cursor(topic, after)
		if frozen != after || after < cursor.Oldest-1 || after > cursor.Head {
			cursor.Status = "resync_required"
			cursor.Reason = "recovery_window_expired"
			state.pending[topic] = cursor.Head
			h.resyncs++
		} else {
			delete(state.pending, topic)
			for _, item := range h.buf[topic] {
				if item.Seq > after && state.allowsMarket(item) {
					replays = append(replays, item)
				}
			}
		}
		cursors[topic] = cursor
	}
	if len(cursors) == 0 {
		return
	}
	// Ack carries the frozen REST boundary, then replay reaches the current head.
	if !h.sendLocked(client, h.control("resync_ack", requestID, map[string]any{"topics": cursors})) {
		return
	}
	required := map[string]topicCursor{}
	for topic, cursor := range cursors {
		if cursor.Status == "resync_required" {
			required[topic] = cursor
		}
	}
	if len(required) > 0 && !h.sendLocked(client, h.control("resync_required", requestID, map[string]any{"topics": required})) {
		return
	}
	h.replayLocked(client, replays, requestID)
}

func (h *Hub) replayLocked(client *websocket.Client, frames []frame, requestID uint64) {
	// Batch avoids overflowing the transport's 100-message queue with a 500-frame ring.
	for start := 0; start < len(frames); start += 25 {
		end := start + 25
		if end > len(frames) {
			end = len(frames)
		}
		batch := make([]*websocket.WResponse, 0, end-start)
		for _, item := range frames[start:end] {
			batch = append(batch, item.response())
		}
		if !h.sendLocked(client, h.control("replay_batch", requestID, map[string]any{"frames": batch})) {
			return
		}
	}
}

func (h *Hub) sendLocked(client *websocket.Client, message *websocket.WResponse) bool {
	if trySend(client, message) {
		return true
	}
	h.slowDisconnects++
	delete(h.clients, client)
	client.SetTopics(nil)
	// Closing the socket wakes both transport goroutines; never silently skip a delta.
	if client.Socket != nil {
		_ = client.Socket.Close()
	}
	return false
}

func (h *Hub) addFrame(item frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq[item.Topic]++
	item.Seq = h.seq[item.Topic]
	item.Epoch = h.epoch
	h.buf[item.Topic] = append(h.buf[item.Topic], item)
	if len(h.buf[item.Topic]) > ringCap {
		h.buf[item.Topic] = append([]frame(nil), h.buf[item.Topic][len(h.buf[item.Topic])-ringCap:]...)
	}
	for client, state := range h.clients {
		if client.IsClosed() {
			delete(h.clients, client)
			continue
		}
		if _, recovering := state.pending[item.Topic]; !recovering && client.Allows(item.Topic) && state.allowsMarket(item) {
			h.sendLocked(client, item.response())
		}
	}
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
	pending := h.market
	h.market = map[string]frame{}
	// Disconnected clients that never completed recovery must not accumulate.
	for client := range h.clients {
		if client.IsClosed() {
			delete(h.clients, client)
		}
	}
	h.mu.Unlock()
	keys := make([]string, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		h.addFrame(pending[key])
	}
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
	{"trade.plan", "position", "plan", "admin_trade_plan", false},
	{"sys.research", "brain", "research", "admin_research", false},
	{events.SubjectRiskAlert, "alert", "alert", "admin_risk_alert", false},
	{events.SubjectKillSwitch, "alert", "alert", "admin_killswitch", false},
	{events.SubjectIntelAlert, "alert", "alert", "admin_intel_alert", false},
	{events.SubjectNotifyDesktop, "alert", "notify", "admin_notify_desktop", false},
	{events.SubjectStrategyReview, "alert", "alert", "admin_strategy_review", false},
	{events.SubjectBriefing, "alert", "alert", "admin_briefing", false},
	{events.SubjectMarketSnapshot, "market", "market", "", true},
	// T13 行情主题上前端 WS：bar 经 onMarketBar 展开进合并键（≥1s/票尾随合并），
	// limit/state 是量小的事件流，直通不合并（marketKey 对这两种 kind 不落键）。
	{events.SubjectMarketBar, "market", "bar", "", true},
	{events.SubjectMarketLimit, "market", "limit", "", true},
	{events.SubjectMarketState, "market", "state", "", true},
	{events.SubjectMarketAlert, "alert", "move", "", true},
	{events.SubjectBrainProgress, "brain", "brain", "", true},
}

func (h *Hub) bind(bus *events.Bus) {
	for _, item := range natsRoutes {
		item := item
		// market.bar.1m 的批量载荷要逐票展开+无 bar 兜底（T13），其余主题走默认通道。
		handle := h.onRawSubject
		if item.subject == events.SubjectMarketBar {
			handle = h.onMarketBar
		}
		if item.core {
			_, err := bus.Conn().Subscribe(item.subject, func(m *nats.Msg) { handle(m.Subject, item.topic, item.event, m.Data) })
			h.observations.binding(item.subject, err == nil, time.Now().UTC())
			if err != nil {
				h.log.Warnf("订阅 %s：%v", item.subject, err)
			}
			continue
		}
		_, err := bus.JetStream().Subscribe(item.subject, func(m *nats.Msg) {
			handle(m.Subject, item.topic, item.event, m.Data)
			_ = m.Ack()
		}, nats.Durable(item.durable), nats.ManualAck(), nats.DeliverNew(),
			nats.AckWait(10*time.Second), nats.MaxDeliver(5))
		h.observations.binding(item.subject, err == nil, time.Now().UTC())
		if err != nil {
			h.log.Warnf("订阅 %s：%v", item.subject, err)
		}
	}
	h.log.Info("桌面推送订阅尝试完成；逐主题绑定状态见观测接口")
}

func (h *Hub) onRaw(topic, event string, body []byte) { h.onRawSubject("", topic, event, body) }

// acceptEnvelope 解析并记账事件信封，产出转发用的帧元数据；校验失败时返回 ok=false。
func (h *Hub) acceptEnvelope(subject, topic string, body []byte) (env events.Envelope, metadata map[string]any, ok bool) {
	at := h.observations.now().UTC()
	env, err := events.Unmarshal(body)
	if err != nil {
		h.observations.reject(subject, topic, len(body), at)
		h.log.Warnf("坏事件 %s：%v", topic, err)
		return events.Envelope{}, nil, false
	}
	if subject != "" && env.Subject != subject {
		h.observations.reject(subject, topic, len(body), at)
		h.log.Warnf("事件主题不匹配：route=%s", subject)
		return events.Envelope{}, nil, false
	}
	h.observations.receive(env, topic, len(body), at)
	raw, marshalErr := json.Marshal(env)
	if marshalErr != nil {
		h.log.Warnf("坏信封 %s：%v", topic, marshalErr)
		return events.Envelope{}, nil, false
	}
	metadata = map[string]any{}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return events.Envelope{}, nil, false
	}
	delete(metadata, "payload")
	metadata["gateway_received_at"] = observationTime(at)
	return env, metadata, true
}

func (h *Hub) onRawSubject(subject, topic, event string, body []byte) {
	env, metadata, ok := h.acceptEnvelope(subject, topic, body)
	if !ok {
		return
	}
	h.publish(topic, event, env.Payload, metadata)
}

// onMarketBar 是 market.bar.1m 的专用入口（T13）：
// {"bars":[...]} 批量载荷展开成单票 bar 帧，经 market 合并键落盘——同一 symbol 同一根线
// 在 1s 刷出窗口内尾随合并，只发最新；载荷无 bar 行（停牌/非交易时段的 meta/控制帧）
// 落成显式 {"meta":{"no_bar":true}} 控制帧，不静默丢弃。
func (h *Hub) onMarketBar(subject, topic, event string, body []byte) {
	env, metadata, ok := h.acceptEnvelope(subject, topic, body)
	if !ok {
		return
	}
	var batch struct {
		Bars []json.RawMessage `json:"bars"`
	}
	if err := json.Unmarshal(env.Payload, &batch); err == nil && len(batch.Bars) > 0 {
		for _, bar := range batch.Bars {
			h.publish(topic, event, bar, metadata)
		}
		return
	}
	if looksLikeBar(env.Payload) {
		h.publish(topic, event, env.Payload, metadata)
		return
	}
	h.publish(topic, event, json.RawMessage(`{"meta":{"no_bar":true}}`), metadata)
}

// looksLikeBar 判定载荷带 OHLC 行；meta/控制帧（无 OHLC 字段）返回 false。
func looksLikeBar(raw json.RawMessage) bool {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	for _, field := range []string{"open", "high", "low", "close"} {
		if _, ok := payload[field]; ok {
			return true
		}
	}
	return false
}

func (f frame) response() *websocket.WResponse {
	payload := decodeRaw(f.Raw)
	event := f.Event
	if event == "" {
		event = f.Topic
	}
	return &websocket.WResponse{
		Event:       event,
		Data:        payload,
		Topic:       f.Topic,
		Seq:         f.Seq,
		Payload:     payload,
		ServerEpoch: f.Epoch,
		TopicSeq:    f.Seq,
		Metadata:    f.Metadata,
	}
}

func trySend(client *websocket.Client, msg *websocket.WResponse) bool { return client.TrySend(msg) }

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
