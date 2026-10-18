package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "server/api/admin/v1"
	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/encoding/protojson"
)

func observationHub(now func() time.Time) *Hub {
	h := NewHub(log.DefaultLogger)
	h.observations = newObservationLedger(h.epoch, now)
	return h
}
func observationBody(t *testing.T, source, subject, trace string, payload any) []byte {
	t.Helper()
	env, err := events.New(source, subject, trace, payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func observationRequest(t *testing.T, g *Gate, path, role string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if role != "" {
		token, err := Issue(g.Settings.Secret, User{Name: "reader", Role: role}, g.now())
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}
func parseObservation(t *testing.T, w *httptest.ResponseRecorder) *v1.GetObservabilityReply {
	t.Helper()
	out := &v1.GetObservabilityReply{}
	if err := protojson.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatalf("decode %v: %s", err, w.Body)
	}
	return out
}

func TestObservationGeneratedHTTPBindingPermissionsAndFilters(t *testing.T) {
	now := time.Date(2026, 10, 15, 1, 30, 0, 0, time.UTC)
	h := observationHub(func() time.Time { return now })
	g := testGate("", nil)
	g.Observations = h
	raw := observationBody(t, "strategy", events.SubjectSignal, "trace-a", map[string]any{"signal_id": "s1", "stock_code": "600000", "status": "approved"})
	h.onRawSubject(events.SubjectSignal, "signal", "signal", raw)
	h.onRawSubject(events.SubjectSignal, "signal", "signal", observationBody(t, "strategy", events.SubjectSignal, "trace-b", map[string]any{"signal_id": "s2"}))
	if got := observationRequest(t, g, "/api/ops/observability", ""); got.Code != 401 {
		t.Fatalf("anonymous %d %s", got.Code, got.Body)
	}
	for _, role := range []string{"viewer", "owner"} {
		w := observationRequest(t, g, "/api/ops/observability?subject=strategy.signal.updated&trace_id=trace-a&limit=1", role)
		if w.Code != 200 {
			t.Fatalf("%s %d %s", role, w.Code, w.Body)
		}
		out := parseObservation(t, w)
		if !out.Available || out.Scope != "admin_gateway_ingress" || out.NatsConnected || len(out.Samples) != 1 || out.Samples[0].TraceId != "trace-a" || len(out.Edges) != 1 || out.Edges[0].ReceivedTotal != 2 {
			t.Fatalf("reply %+v", out)
		}
		if out.StatisticsScope != "subject_source_counters;trace_id_filters_samples_only" {
			t.Fatal("trace scope missing")
		}
	}
	for _, query := range []string{"subject=trade.%3E", "trace_id=%3Csecret%3E", "limit=-1", "limit=invalid"} {
		w := observationRequest(t, g, "/api/ops/observability?"+query, "viewer")
		if w.Code != 400 {
			t.Fatalf("query %s => %d %s", query, w.Code, w.Body)
		}
	}
	w := observationRequest(t, g, "/api/ops/observability?limit=999", "viewer")
	if w.Code != 200 {
		t.Fatalf("limit cap %d", w.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/ops/observability", nil)
	res := httptest.NewRecorder()
	g.ServeHTTP(res, req)
	if res.Code != 405 {
		t.Fatalf("write route %d", res.Code)
	}
	g.Observations = nil
	out := parseObservation(t, observationRequest(t, g, "/api/ops/observability", "viewer"))
	if out.Available || out.Note == "" || len(out.Edges) != 0 {
		t.Fatal("unwired hub fabricated metrics")
	}
}

func TestObservationBeforeCoalescingTimeAndByteBasis(t *testing.T) {
	now := time.Date(2026, 10, 15, 1, 30, 0, 0, time.UTC)
	h := observationHub(func() time.Time { return now })
	totalBytes := 0
	for i := 0; i < 3; i++ {
		raw := observationBody(t, "datahub", events.SubjectMarketSnapshot, "trace", map[string]any{"count": i, "as_of": "2026-10-15T01:29:59Z"})
		totalBytes += len(raw)
		h.onRawSubject(events.SubjectMarketSnapshot, "market", "market", raw)
	}
	h.flushMarket()
	if len(h.buf["market"]) != 1 {
		t.Fatal("test did not exercise coalescing")
	}
	out := h.ObservationSnapshot(events.SubjectMarketSnapshot, "", 80)
	if out.WindowComplete || out.WindowObservedSeconds != 0 {
		t.Fatal("startup coverage was invented")
	}
	if len(out.Edges) != 1 || out.Edges[0].ReceivedTotal != 3 || out.Edges[0].AcceptedTotal != 3 || out.Edges[0].BytesTotal != uint64(totalBytes) || out.Edges[0].WindowCount != 3 {
		t.Fatalf("counts %+v", out.Edges)
	}
	if out.Samples[0].SourceTime != "" || out.Samples[0].EventTime == "" || out.Samples[0].ReceivedAt != observationTime(now) {
		t.Fatal("unknown source time was substituted")
	}
	now = now.Add(60 * time.Second)
	out = h.ObservationSnapshot("", "", 80)
	if !out.WindowComplete || out.WindowObservedSeconds != 60 {
		t.Fatal("observed window coverage missing")
	}
	if out.Edges[0].WindowCount != 0 || out.Edges[0].ReceivedTotal != 3 || len(out.Samples) != 0 {
		t.Fatalf("window %+v", out)
	}
}

func TestObservationRegistrationDoesNotPretendBindingOrBusinessReadiness(t *testing.T) {
	now := time.Date(2026, 10, 15, 1, 30, 0, 0, time.UTC)
	h := observationHub(func() time.Time { return now })
	initial := h.ObservationSnapshot("", "", 80)
	for _, route := range initial.RegisteredRoutes {
		if route.BindingState != "declared" || route.BindingUpdatedAt != "" {
			t.Fatal("unattempted route claimed bound")
		}
	}
	h.observations.binding(events.SubjectSignal, true, now)
	h.observations.binding(events.SubjectBrainProgress, false, now)
	out := h.ObservationSnapshot("", "", 80)
	if out.NatsConnected || len(out.Edges) != 0 {
		t.Fatal("binding fabricated connection or traffic")
	}
	for _, route := range out.RegisteredRoutes {
		if route.Observation != "gateway_receive_only" {
			t.Fatal("coverage overstated")
		}
		if route.Subject == events.SubjectSignal && (route.BindingState != "bound" || route.BindingUpdatedAt == "") {
			t.Fatal("successful binding missing")
		}
		if route.Subject == events.SubjectBrainProgress && route.BindingState != "failed" {
			t.Fatal("failed binding missing")
		}
	}
}

func TestObservationRedactionIsDenyByDefaultAndDoesNotRetainPayload(t *testing.T) {
	now := time.Date(2026, 10, 15, 1, 30, 0, 0, time.UTC)
	h := observationHub(func() time.Time { return now })
	raw := observationBody(t, "trade", events.SubjectTradeAccount, "trace-safe", map[string]any{"account_id": "PRIVATE-ACCOUNT", "cash": 123456.78, "available": 999999, "holdings": []any{map[string]any{"symbol": "600000", "qty": 10000}}, "positions": []any{"PRIVATE-POSITION"}, "api_key": "SECRET-API", "password": "SECRET-PASS", "token": "SECRET-TOKEN", "reason": "private free text", "title": "secret title", "status": "SECRET-STATUS", "symbol": "600000", "account_type": "SIM", "stale": false})
	var env events.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	env.EntityID = "PRIVATE-ACCOUNT"
	raw, _ = env.Marshal()
	h.onRawSubject(events.SubjectTradeAccount, "position", "position", raw)
	out := h.ObservationSnapshot("", "", 80)
	body, _ := protojson.Marshal(out)
	for _, private := range []string{"PRIVATE-ACCOUNT", "123456.78", "999999", "10000", "PRIVATE-POSITION", "SECRET-API", "SECRET-PASS", "SECRET-TOKEN", "private free text", "secret title", "SECRET-STATUS", "holdings", "positions", "password", "api_key"} {
		if strings.Contains(string(body), private) {
			t.Fatalf("leaked %q in %s", private, body)
		}
	}
	sample := out.Samples[0]
	if sample.EntityId != "" || sample.Payload["symbol"] != "600000" || sample.Payload["account_type"] != "SIM" || sample.Payload["stale"] != "false" {
		t.Fatalf("projection %+v", sample)
	}
	sample.Payload["symbol"] = "mutated"
	if h.ObservationSnapshot("", "", 80).Samples[0].Payload["symbol"] != "600000" {
		t.Fatal("caller mutated retained sample")
	}
	_, status := projectObservationPayload(json.RawMessage(`{"status":"` + strings.Repeat("x", observationPayloadLimit) + `"}`))
	if status != "oversized_omitted" {
		t.Fatal("large payload retained")
	}
}

func TestObservationRejectedEnvelopesCountWithoutLeakingRawData(t *testing.T) {
	now := time.Date(2026, 10, 15, 1, 30, 0, 0, time.UTC)
	h := observationHub(func() time.Time { return now })
	h.onRawSubject(events.SubjectRiskAlert, "alert", "alert", []byte(`{"api_key":"SECRET-RAW"}`))
	valid := observationBody(t, "risk", events.SubjectRiskAlert, "trace", map[string]any{"level": "critical"})
	var bad map[string]any
	_ = json.Unmarshal(valid, &bad)
	bad["schema_version"] = 999
	raw, _ := json.Marshal(bad)
	h.onRawSubject(events.SubjectRiskAlert, "alert", "alert", raw)
	wrong := observationBody(t, "trade", events.SubjectTradeOrder, "trace", map[string]any{"status": "accepted"})
	h.onRawSubject(events.SubjectRiskAlert, "alert", "alert", wrong)
	h.onRawSubject(events.SubjectRiskAlert, "alert", "alert", valid)
	out := h.ObservationSnapshot("", "", 80)
	if out.ErrorsTotal != 3 || len(out.Samples) != 1 {
		t.Fatalf("errors %+v", out)
	}
	var received, accepted, windowErrors uint64
	for _, edge := range out.Edges {
		received += edge.ReceivedTotal
		accepted += edge.AcceptedTotal
		windowErrors += edge.WindowErrors
	}
	if received != 4 || accepted != 1 || windowErrors != 3 {
		t.Fatalf("counts %+v", out.Edges)
	}
	encoded, _ := protojson.Marshal(out)
	if strings.Contains(string(encoded), "SECRET-RAW") {
		t.Fatal("bad envelope raw leaked")
	}
	if len(h.buf["alert"]) != 1 {
		t.Fatal("rejected envelope reached WS")
	}
}

func TestObservationConcurrentIngressCapacityAndSnapshots(t *testing.T) {
	now := time.Date(2026, 10, 15, 1, 30, 0, 0, time.UTC)
	h := observationHub(func() time.Time { return now })
	raw := observationBody(t, "strategy", events.SubjectSignal, "trace", map[string]any{"signal_id": "s1", "stock_code": "600000"})
	var wg sync.WaitGroup
	for worker := 0; worker < 10; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				h.onRawSubject(events.SubjectSignal, "signal", "signal", raw)
			}
		}()
	}
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = h.ObservationSnapshot("", "", 20)
			}
		}()
	}
	wg.Wait()
	out := h.ObservationSnapshot("", "", 80)
	if len(out.Samples) != 80 || !out.SamplesTruncated || out.Edges[0].ReceivedTotal != 1000 || h.observations.length != 80 {
		t.Fatalf("capacity %+v", out)
	}
	if limit := h.ObservationSnapshot("", "", 5); len(limit.Samples) != 5 || !limit.SamplesTruncated {
		t.Fatal("query limit not bounded")
	}
	for _, route := range natsRoutes {
		for source := range observationSources {
			h.observations.receive(events.Envelope{Subject: route.subject, Source: source}, route.topic, 1, now)
		}
		h.observations.receive(events.Envelope{Subject: route.subject, Source: "untrusted"}, route.topic, 1, now)
	}
	out = h.ObservationSnapshot("", "", 80)
	if len(h.observations.rows) > observationEdgeCapacity || out.CounterOverflowTotal == 0 || out.CounterCapacity != 128 {
		t.Fatalf("counter capacity %d %+v", len(h.observations.rows), out)
	}
}
