package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"server/pkg/events"
	"server/utils/websocket"

	"github.com/go-kratos/kratos/v2/log"
	gorilla "github.com/gorilla/websocket"
)

func testClient(size int) *websocket.Client {
	return &websocket.Client{Send: make(chan *websocket.WResponse, size)}
}
func drain(c *websocket.Client) []*websocket.WResponse {
	out := []*websocket.WResponse{}
	for {
		select {
		case m := <-c.Send:
			out = append(out, m)
		default:
			return out
		}
	}
}
func framesOf(messages []*websocket.WResponse) []*websocket.WResponse {
	out := []*websocket.WResponse{}
	for _, m := range messages {
		if m.Topic == "replay_batch" {
			out = append(out, m.Payload.(map[string]any)["frames"].([]*websocket.WResponse)...)
		} else if m.Seq > 0 {
			out = append(out, m)
		}
	}
	return out
}
func cursorsOf(m *websocket.WResponse) map[string]topicCursor {
	return m.Payload.(map[string]any)["topics"].(map[string]topicCursor)
}
func subscribeReady(h *Hub, c *websocket.Client, topics []string) {
	resume := map[string]int64{}
	for _, t := range topics {
		resume[t] = h.seq[t]
	}
	h.beginSubscription(c, topics, resume, h.epoch, 1, false, nil)
	drain(c)
}

func TestHubMetadataAndCompatiblePayload(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	env, err := events.New("strategy", events.SubjectSignal, "trace-7", map[string]any{"signal_id": "s1", "stock_code": "600000"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := env.Marshal()
	h.onRaw("signal", "signal", raw)
	c := testClient(8)
	h.beginSubscription(c, []string{"signal"}, map[string]int64{"signal": 0}, h.epoch, 1, false, nil)
	frames := framesOf(drain(c))
	if len(frames) != 1 {
		t.Fatalf("frames %+v", frames)
	}
	m := frames[0]
	if m.Seq != 1 || m.TopicSeq != 1 || m.ServerEpoch != h.epoch {
		t.Fatalf("cursor %+v", m)
	}
	if m.Metadata["event_id"] != env.EventID || m.Metadata["trace_id"] != "trace-7" || m.Metadata["subject"] != events.SubjectSignal {
		t.Fatalf("metadata %+v", m.Metadata)
	}
	if _, ok := m.Metadata["payload"]; ok {
		t.Fatal("payload duplicated in metadata")
	}
	if m.Metadata["gateway_received_at"] == nil {
		t.Fatal("no gateway timestamp")
	}
	if m.Payload.(map[string]any)["stock_code"] != "600000" {
		t.Fatal("payload changed")
	}
}
func TestHubRestartRejectsOldHighCursor(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	h.Publish("signal", "signal", 1)
	c := testClient(8)
	h.beginSubscription(c, []string{"signal"}, map[string]int64{"signal": 1000}, "old-admin", 4, false, nil)
	messages := drain(c)
	if len(messages) != 2 || messages[0].Topic != "sub_ack" || messages[1].Topic != "resync_required" {
		t.Fatalf("messages %+v", messages)
	}
	cursor := cursorsOf(messages[1])["signal"]
	if cursor.Head != 1 || cursor.Oldest != 1 || cursor.Reason != "epoch_changed" {
		t.Fatalf("cursor %+v", cursor)
	}
	h.completeRecovery(c, map[string]int64{"signal": 1}, h.epoch, 4)
	if cursorsOf(drain(c)[0])["signal"].Status != "resumed" {
		t.Fatal("recovery failed")
	}
	h.Publish("signal", "signal", 2)
	if got := drain(c); len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("frames %+v", got)
	}
}
func TestHubReplayExpiredAndCursorAhead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		after  int64
		reason string
	}{{"expired", 0, "replay_expired"}, {"ahead", 1000, "cursor_ahead"}} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHub(log.DefaultLogger)
			for i := 0; i < 501; i++ {
				h.Publish("signal", "signal", i)
			}
			c := testClient(8)
			h.beginSubscription(c, []string{"signal"}, map[string]int64{"signal": tc.after}, h.epoch, 1, false, nil)
			messages := drain(c)
			cursor := cursorsOf(messages[1])["signal"]
			if cursor.Reason != tc.reason || cursor.Head != 501 || cursor.Oldest != 2 || len(framesOf(messages)) != 0 {
				t.Fatalf("cursor %+v", cursor)
			}
		})
	}
}
func TestHubSnapshotWindowReplayAndExpiry(t *testing.T) {
	for _, count := range []int{500, 501} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			h := NewHub(log.DefaultLogger)
			c := testClient(100)
			h.beginSubscription(c, []string{"position"}, nil, "", 9, false, nil)
			drain(c)
			for i := 0; i < count; i++ {
				h.Publish("position", "position", i)
			}
			if len(c.Send) != 0 {
				t.Fatal("delta escaped before REST ack")
			}
			h.completeRecovery(c, map[string]int64{"position": 0}, h.epoch, 9)
			messages := drain(c)
			if count == 501 {
				if cursorsOf(messages[0])["position"].Reason != "recovery_window_expired" || messages[1].Topic != "resync_required" {
					t.Fatalf("messages %+v", messages)
				}
				if len(framesOf(messages)) != 0 {
					t.Fatal("partial replay")
				}
				h.completeRecovery(c, map[string]int64{"position": 501}, h.epoch, 9)
				if cursorsOf(drain(c)[0])["position"].Status != "resumed" {
					t.Fatal("second recovery failed")
				}
				return
			}
			frames := framesOf(messages)
			if len(frames) != count {
				t.Fatalf("replayed %d", len(frames))
			}
			for i, m := range frames {
				if m.Seq != int64(i+1) {
					t.Fatalf("order %d/%d", i, m.Seq)
				}
			}
		})
	}
}
func TestHubAckAndPublishHandoffHasNoGap(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	c := testClient(200)
	h.beginSubscription(c, []string{"position"}, nil, "", 10, false, nil)
	drain(c)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); h.completeRecovery(c, map[string]int64{"position": 0}, h.epoch, 10) }()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			h.Publish("position", "position", i)
		}
	}()
	wg.Wait()
	frames := framesOf(drain(c))
	if len(frames) != 100 {
		t.Fatalf("lost frames %d", len(frames))
	}
	for i, m := range frames {
		if m.Seq != int64(i+1) {
			t.Fatalf("order %d/%d", i, m.Seq)
		}
	}
}
func TestHubMarketCoalescesBySecurityAndType(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	h.Publish("market", "tick", map[string]any{"symbol": "600000", "last": 1})
	h.Publish("market", "tick", map[string]any{"symbol": "600000", "last": 2})
	h.Publish("market", "tick", map[string]any{"symbol": "000001", "last": 3})
	h.Publish("market", "bar", map[string]any{"symbol": "600000", "last": 4})
	h.flushMarket()
	if len(h.buf["market"]) != 3 {
		t.Fatalf("frames %+v", h.buf["market"])
	}
	found := map[string]float64{}
	for _, item := range h.buf["market"] {
		var payload map[string]any
		_ = json.Unmarshal(item.Raw, &payload)
		found[item.Event+payload["symbol"].(string)] = payload["last"].(float64)
	}
	if found["tick600000"] != 2 || found["tick000001"] != 3 || found["bar600000"] != 4 {
		t.Fatalf("market %+v", found)
	}
	h.Publish("market", "unknown", 1)
	h.Publish("market", "unknown", 2)
	if len(h.buf["market"]) != 5 {
		t.Fatal("unknown event coalesced")
	}
	for i := 0; i < 20; i++ {
		h.Publish("alert", "alert", i)
	}
	if len(h.buf["alert"]) != 20 {
		t.Fatal("critical events coalesced")
	}
}
func TestHubSlowClientIsRemovedRatherThanDropping(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	c := testClient(1)
	subscribeReady(h, c, []string{"alert"})
	h.Publish("alert", "alert", 1)
	h.Publish("alert", "alert", 2)
	if h.slowDisconnects != 1 || c.Allows("alert") || len(h.clients) != 0 {
		t.Fatalf("slow client retained %+v", h.Stats())
	}
	if len(h.buf["alert"]) != 2 {
		t.Fatal("event lost from ring")
	}
}
func TestHubIndependentWindowsAndStaleAcknowledgement(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	one, two := testClient(8), testClient(8)
	h.beginSubscription(one, []string{"signal"}, nil, "", 1, false, nil)
	drain(one)
	subscribeReady(h, two, []string{"signal"})
	h.Publish("signal", "signal", 1)
	if len(one.Send) != 0 || len(two.Send) != 1 {
		t.Fatal("shared recovery state")
	}
	h.beginSubscription(one, []string{"alert"}, nil, "", 2, false, nil)
	drain(one)
	h.completeRecovery(one, map[string]int64{"signal": 0}, h.epoch, 1)
	if len(one.Send) != 0 || one.Allows("signal") {
		t.Fatal("stale ack changed subscription")
	}
}
func TestHubOnTextCompatibilityAndTopicFilter(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	c := testClient(8)
	if !h.onText(c, []byte(`{"op":"sub","topics":["alert","nope"],"resume":{"alert":1000},"request_id":3}`)) {
		t.Fatal("not consumed")
	}
	messages := drain(c)
	if len(cursorsOf(messages[0])) != 1 || messages[1].Topic != "resync_required" {
		t.Fatalf("messages %+v", messages)
	}
	if h.onText(c, []byte(`{"e":"ping"}`)) || h.onText(c, []byte(`not-json`)) {
		t.Fatal("legacy or bad frame consumed")
	}
}

func TestHubSlowClientClosesActualSocket(t *testing.T) {
	accepted := make(chan *gorilla.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := gorilla.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			accepted <- conn
		}
	}))
	defer server.Close()
	peer, _, err := gorilla.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	conn := <-accepted
	defer conn.Close()
	h := NewHub(log.DefaultLogger)
	client := testClient(1)
	client.Socket = conn
	subscribeReady(h, client, []string{"alert"})
	h.Publish("alert", "alert", 1)
	h.Publish("alert", "alert", 2)
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := peer.ReadMessage(); err == nil {
		t.Fatal("slow socket remained open")
	}
}
func TestHubMarketTimeBucketsAndTransitionsAreNotErased(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	for _, minute := range []string{"09:31", "09:32"} {
		h.publish("market", "market", asRaw(map[string]any{"symbol": "600000", "time": minute}), map[string]any{"subject": events.SubjectMarketBar})
	}
	for i := 0; i < 2; i++ {
		h.publish("market", "market", asRaw(map[string]any{"symbol": "600000", "state": i}), map[string]any{"subject": events.SubjectMarketLimit})
	}
	h.flushMarket()
	if len(h.buf["market"]) != 4 {
		t.Fatalf("lost bar bucket or transition: %d", len(h.buf["market"]))
	}
}

func subscribeScoped(h *Hub, c *websocket.Client, topics []string, filters []marketFilter) {
	resume := map[string]int64{}
	for _, t := range topics {
		resume[t] = h.seq[t]
	}
	h.beginSubscription(c, topics, resume, h.epoch, 1, false, filters)
	drain(c)
}

// P0-3：声明 symbol+freq 兴趣的客户端只收到该证券该周期的帧；快照 meta 与未声明客户端不受影响。
func TestHubMarketFilterNarrowsBySecurity(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	all, scoped := testClient(16), testClient(16)
	subscribeReady(h, all, []string{"market"})
	subscribeScoped(h, scoped, []string{"market"}, []marketFilter{{Symbol: "600000", Freq: "1m"}})
	h.Publish("market", "bar", map[string]any{"symbol": "600000", "freq": "1m", "time": "09:31", "close": 1})
	h.Publish("market", "bar", map[string]any{"symbol": "600000", "freq": "1d", "time": "2026-10-16", "close": 2})
	h.Publish("market", "bar", map[string]any{"symbol": "000001", "freq": "1m", "time": "09:31", "close": 3})
	h.publish("market", "market", asRaw(map[string]any{"count": 3}), map[string]any{"subject": events.SubjectMarketSnapshot})
	h.flushMarket()
	scopedFrames := framesOf(drain(scoped))
	if len(scopedFrames) != 2 {
		t.Fatalf("filtered client frames %+v", scopedFrames)
	}
	if payload := scopedFrames[0].Payload.(map[string]any); payload["symbol"] != "600000" || payload["freq"] != "1m" {
		t.Fatalf("unexpected frame passed filter %+v", payload)
	}
	if got := framesOf(drain(all)); len(got) != 4 {
		t.Fatalf("unfiltered client lost frames: %d", len(got))
	}
}

// P0-3：只给 symbol 的过滤器收该证券全部周期；freq 缺失的帧按证券级放行（宁多勿丢）。
func TestHubMarketFilterWildcardFreqAndUnknownFreq(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	scoped := testClient(16)
	subscribeScoped(h, scoped, []string{"market"}, []marketFilter{{Symbol: "600000"}})
	h.Publish("market", "tick", map[string]any{"symbol": "600000", "last": 1})
	h.Publish("market", "bar", map[string]any{"symbol": "600000", "freq": "1m", "time": "09:31"})
	h.Publish("market", "bar", map[string]any{"symbol": "600000", "freq": "1d", "time": "2026-10-16"})
	h.Publish("market", "tick", map[string]any{"symbol": "000001", "last": 2})
	h.flushMarket()
	if got := framesOf(drain(scoped)); len(got) != 3 {
		t.Fatalf("wildcard filter frames %d", len(got))
	}
}

// P0-3：断线重连的回放同样按兴趣过滤，不会把别的证券补推给收窄客户端。
func TestHubMarketFilterAppliesToReplay(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	h.Publish("market", "bar", map[string]any{"symbol": "600000", "freq": "1m", "time": "09:31"})
	h.Publish("market", "bar", map[string]any{"symbol": "000001", "freq": "1m", "time": "09:31"})
	h.flushMarket()
	c := testClient(16)
	h.beginSubscription(c, []string{"market"}, map[string]int64{"market": 0}, h.epoch, 1, false, []marketFilter{{Symbol: "600000", Freq: "1m"}})
	got := framesOf(drain(c))
	if len(got) != 1 || got[0].Payload.(map[string]any)["symbol"] != "600000" {
		t.Fatalf("replay not filtered %+v", got)
	}
}

// P0-3：过滤器条目全缺 symbol 时视为无效声明，回退全量而不是饿死连接。
func TestHubMarketFilterEmptyEntriesFallBackToAll(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	scoped := testClient(16)
	subscribeScoped(h, scoped, []string{"market"}, []marketFilter{{Symbol: ""}, {Freq: "1m"}})
	h.Publish("market", "tick", map[string]any{"symbol": "000001", "last": 1})
	h.flushMarket()
	if got := framesOf(drain(scoped)); len(got) != 1 {
		t.Fatalf("invalid filter starved client: %d", len(got))
	}
}
