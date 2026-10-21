package core

import (
	"context"
	"encoding/json"
	"testing"

	"server/app/trade/internal/biz"
	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/nats-io/nats.go"
)

func signalMessage(t *testing.T, source, subject string) *nats.Msg {
	t.Helper()
	env, err := events.New(source, subject, "signal-auto-test", map[string]any{
		"signal_id": 11, "book": "paper", "status": "pending", "stock_code": "600519.SH",
		"strategy": "first_board_weak2strong", "signal_type": "buy", "final_score": 85, "ai_degraded": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return &nats.Msg{Subject: subject, Data: raw}
}

// 消费失败必须按设计「critical 进 notify」——沿用现有 notify.request 发布客户端，
// 不静默吞掉（一切资产类异常必须留人工核对口）。
func TestSignalAutoPublishesCriticalOnFailure(t *testing.T) {
	store := biz.NewMemStore()
	e := biz.NewEngine(store, allowCheck{}, biz.Settings{}, log.DefaultLogger)
	var notes []events.Envelope
	r := &Runner{
		e:   e,
		log: log.NewHelper(log.DefaultLogger),
		publish: func(ctx context.Context, env events.Envelope) error {
			notes = append(notes, env)
			return nil
		},
	}
	r.onSignal(signalMessage(t, "strategy", events.SubjectSignal))
	if len(notes) != 1 || notes[0].Subject != events.SubjectNotifyRequest {
		t.Fatalf("expected one notify.request, got %+v", notes)
	}
	var body map[string]any
	if err := json.Unmarshal(notes[0].Payload, &body); err != nil {
		t.Fatal(err)
	}
	if body["level"] != "critical" || body["category"] != "trade" || body["dedup_key"] != "trade:auto-plan:11" {
		t.Fatalf("notify payload missing required identity: %+v", body)
	}
}

// 非 strategy 来源/非信号主题的信封直接丢弃（ack 给 durable），不消费、不告警。
func TestSignalAutoRejectsUntrustedEnvelope(t *testing.T) {
	store := biz.NewMemStore()
	e := biz.NewEngine(store, allowCheck{}, biz.Settings{}, log.DefaultLogger)
	var notes []events.Envelope
	r := &Runner{
		e:   e,
		log: log.NewHelper(log.DefaultLogger),
		publish: func(ctx context.Context, env events.Envelope) error {
			notes = append(notes, env)
			return nil
		},
	}
	r.onSignal(signalMessage(t, "intel", events.SubjectSignal))
	r.onSignal(signalMessage(t, "strategy", events.SubjectMarketState))
	if len(notes) != 0 {
		t.Fatalf("untrusted envelope reached notify: %+v", notes)
	}
}

// 过滤后的事件（非买方向）成功处理完即 ack：无告警、无意图占位。
func TestSignalAutoFilteredEventSkipsSilently(t *testing.T) {
	store := biz.NewMemStore()
	e := biz.NewEngine(store, allowCheck{}, biz.Settings{}, log.DefaultLogger)
	r := &Runner{e: e, log: log.NewHelper(log.DefaultLogger)}
	var notes int
	r.publish = func(ctx context.Context, env events.Envelope) error { notes++; return nil }
	body, err := json.Marshal(map[string]any{
		"signal_id": 12, "book": "paper", "status": "pending", "stock_code": "600519.SH",
		"strategy": "first_board_weak2strong", "signal_type": "sell", "final_score": 85,
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := events.New("strategy", events.SubjectSignal, "signal-auto-test", json.RawMessage(body))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	r.onSignal(&nats.Msg{Subject: events.SubjectSignal, Data: raw})
	if notes != 0 {
		t.Fatalf("filtered event reached notify")
	}
}
