package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	env, err := New("trade", SubjectTradeOrder, "trace-1", map[string]any{"symbol": "600519.SH"})
	if err != nil {
		t.Fatal(err)
	}
	if env.EventID == "" || env.Time.IsZero() {
		t.Fatalf("envelope = %+v", env)
	}
	b, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.EventID != env.EventID || got.TraceID != "trace-1" || got.Subject != SubjectTradeOrder {
		t.Fatalf("got %+v", got)
	}
	var payload map[string]string
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["symbol"] != "600519.SH" {
		t.Fatalf("payload = %s", got.Payload)
	}
}

func TestRejectsEmptySource(t *testing.T) {
	if _, err := New(" ", SubjectSignal, "", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestTraceIDFromContext(t *testing.T) {
	ctx := WithTraceID(context.Background(), "abc")
	if TraceID(ctx) != "abc" {
		t.Fatal(TraceID(ctx))
	}
}

func TestPersistentSubjects(t *testing.T) {
	if !Persistent(SubjectKillSwitch) || !Persistent(SubjectNotifyRequest) {
		t.Fatal("business subjects must be persistent")
	}
	if Persistent(SubjectMarketSnapshot) {
		t.Fatal("market snapshot must stay on core NATS")
	}
	if Persistent(SubjectBrainProgress) || !Persistent(SubjectBriefing) {
		t.Fatal("brain.progress stays on core NATS; strategy.briefing goes to JetStream")
	}
}

func TestSeenDropsDuplicates(t *testing.T) {
	seen := NewSeen(2)
	if !seen.First("a") || seen.First("a") {
		t.Fatal("duplicate was treated as new")
	}
	if !seen.First("b") || !seen.First("c") {
		t.Fatal("new ids were rejected")
	}
	if !seen.First("a") {
		t.Fatal("evicted id should be accepted again")
	}
	if seen.First("") {
		t.Fatal("empty id should not pass")
	}
}

func TestUnmarshalRejectsIncomplete(t *testing.T) {
	b, _ := json.Marshal(Envelope{Time: time.Now(), Subject: SubjectSignal})
	if _, err := Unmarshal(b); err == nil {
		t.Fatal("expected error")
	}
}

func TestEnvelopeVersionAndLegacy(t *testing.T) {
	legacy := []byte(`{"event_id":"old","trace_id":"t","time":"2026-10-15T00:00:00Z","source":"trade","subject":"trade.account","payload":{}}`)
	if _, err := Unmarshal(legacy); err != nil {
		t.Fatalf("legacy envelope must remain readable: %v", err)
	}
	for _, version := range []int{-1, 2, 99} {
		env, _ := New("trade", SubjectTradeAccount, "t", map[string]any{})
		env.SchemaVersion = version
		if _, err := env.Marshal(); err == nil {
			t.Fatalf("unsupported version %d accepted", version)
		}
		raw, _ := json.Marshal(env)
		if _, err := Unmarshal(raw); err == nil {
			t.Fatalf("unsupported version %d decoded", version)
		}
	}
}

func TestCausationAndKnownSourceTime(t *testing.T) {
	parent, _ := New("datahub", SubjectMarketSnapshot, "trace-root", map[string]any{"batch": "a"})
	stamp := time.Date(2026, 10, 15, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	parent.SourceTime = &stamp
	parent.CorrelationID = "batch-a"
	ctx := ContextWithEnvelope(context.Background(), parent)
	child, err := NewWithContext(ctx, "market", SubjectMarketFactor, map[string]any{"symbol": "600519.SH"})
	if err != nil {
		t.Fatal(err)
	}
	if child.EventID == parent.EventID || child.CausationID != parent.EventID || child.TraceID != parent.TraceID || child.CorrelationID != "batch-a" {
		t.Fatalf("child identities = %+v", child)
	}
	if child.SourceTime == nil || !child.SourceTime.Equal(stamp) || child.SourceTime.Location() != time.UTC {
		t.Fatalf("source_time not preserved: %+v", child.SourceTime)
	}
	root, _ := NewWithContext(context.Background(), "datahub", SubjectMarketSnapshot, nil)
	if root.SourceTime != nil || root.ReceivedAt != nil || root.PublishedAt != nil || root.CausationID != "" {
		t.Fatalf("unknown clocks/parent must not be invented: %+v", root)
	}
	child.SourceTime = nil
	if parent.SourceTime == nil {
		t.Fatal("child changed parent")
	}
}

func TestEnvelopePreservesOptionalMetadata(t *testing.T) {
	env, _ := New("strategy", SubjectSignal, "trace", json.RawMessage(`{"signal_id":"s1"}`))
	stamp := time.Now().UTC()
	env.EntityID, env.EntityVersion = "s1", 3
	env.ReceivedAt = &stamp
	raw, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || got.EntityID != "s1" || got.EntityVersion != 3 || got.ReceivedAt == nil || !got.ReceivedAt.Equal(stamp) {
		t.Fatalf("metadata lost: %+v", got)
	}
	env.EntityVersion = -1
	if _, err := env.Marshal(); err == nil {
		t.Fatal("negative entity version accepted")
	}
}

func TestParentEnvelopeReadOnlyLookup(t *testing.T) {
	if _, ok := ParentEnvelope(context.Background()); ok {
		t.Fatal("background ctx has no parent envelope")
	}
	// 只有链号没有父信封：ParentEnvelope 不得把链号当父。
	if _, ok := ParentEnvelope(WithTraceID(context.Background(), "trace-only")); ok {
		t.Fatal("trace-only ctx is not a causation parent")
	}
	parent, err := New("risk", "risk.alert", "trace-risk-7", map[string]any{"k": 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := ContextWithEnvelope(context.Background(), parent)
	got, ok := ParentEnvelope(ctx)
	if !ok || got.EventID != parent.EventID || got.TraceID != parent.TraceID {
		t.Fatalf("parent lookup: %+v ok=%v", got, ok)
	}
}

// New 是诚实的纯构造：链号给什么是什么，绝不发明。这条契约一旦被
// 「空链自动发链」岔口翻转，本测试会红——翻转动作本身即一次可评审的合同变更。
func TestNewDoesNotInventTrace(t *testing.T) {
	env, err := New("trade", SubjectTradeOrder, "", map[string]any{"symbol": "600519.SH"})
	if err != nil {
		t.Fatal(err)
	}
	if env.TraceID != "" || env.CausationID != "" {
		t.Fatalf("root event must stay naked: %+v", env)
	}
	// NewWithContext 在无父 ctx 上逐字退化为 New：同样不发明。
	env2, err := NewWithContext(context.Background(), "trade", SubjectTradeOrder, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if env2.TraceID != "" || env2.CausationID != "" {
		t.Fatalf("parentless ctor must match New: %+v", env2)
	}
}

// OpenTrace 是入口处的幂等开链：无链开合法 uuid、逐次新鲜；已有链
// （调用方串链/级联回放透传/父信封链）一律尊重，不另开、不编父。
func TestOpenTraceOpensRespectsAndStaysFresh(t *testing.T) {
	first, second := OpenTrace(context.Background()), OpenTrace(context.Background())
	t1, t2 := TraceID(first), TraceID(second)
	if t1 == "" || t2 == "" || t1 == t2 {
		t.Fatalf("fresh traces: %q %q", t1, t2)
	}
	if _, err := uuid.Parse(t1); err != nil {
		t.Fatalf("trace not a uuid: %q", t1)
	}
	if got := TraceID(OpenTrace(WithTraceID(context.Background(), "caller-chain"))); got != "caller-chain" {
		t.Fatalf("caller chain not respected: %q", got)
	}
	parent, err := New("intel", SubjectIntelAlert, "parent-trace-9", map[string]any{"k": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := TraceID(OpenTrace(ContextWithEnvelope(context.Background(), parent))); got != "parent-trace-9" {
		t.Fatalf("parent trace not respected: %q", got)
	}
}
