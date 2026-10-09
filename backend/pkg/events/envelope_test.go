package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"
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
