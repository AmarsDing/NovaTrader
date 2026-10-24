package core

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"server/app/notify/internal/biz"
	"server/pkg/events"
	"server/pkg/observe"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/nats-io/nats.go"
)

type observationStore struct {
	biz.Store
	row biz.Message
	err error
}

func (s *observationStore) Insert(_ context.Context, m biz.Message) (biz.Message, bool, error) {
	m.ID, m.Status = 1, biz.StatusPending
	s.row = m
	return m, false, s.err
}
func (*observationStore) Recent(context.Context, string, time.Time, int) (*biz.Message, error) {
	return nil, nil
}
func (s *observationStore) Get(context.Context, int) (biz.Message, error) { return s.row, nil }
func (s *observationStore) SaveDelivery(_ context.Context, m biz.Message) error {
	s.row = m
	return nil
}

type failedChannel struct{}

func (failedChannel) Push(context.Context, biz.DesktopNote) error {
	return errors.New("isolated channel failure")
}
func (failedChannel) Send(context.Context, string) error {
	return errors.New("isolated channel failure")
}

func TestObservationSeparatesPipelineCompletionFromChannelDelivery(t *testing.T) {
	st := &observationStore{}
	p := biz.NewPipeline(biz.Config{RetryMax: 1}, st, failedChannel{}, failedChannel{})
	r := NewRunner(p, nil, nil, nil, log.NewStdLogger(io.Discard))
	reg := observe.ForService("notify")
	before := reg.Snapshot()
	env, err := events.New("risk", events.SubjectRiskAlert, "isolated-trace", map[string]string{"kind": "breaker", "message": "isolated fixture"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	r.onMsg(context.Background(), &nats.Msg{Data: raw}, false)
	if st.row.DesktopStatus != biz.ChannelFailed || st.row.FeishuStatus != biz.ChannelFailed || st.row.Status != biz.StatusFailed {
		t.Fatalf("real pipeline channel failures were lost: %+v", st.row)
	}
	var oldSuccess, oldFail, oldReject uint64
	for _, op := range before.Operations {
		if op.Key == "message_handle" {
			oldSuccess, oldFail, oldReject = op.Total.Succeeded, op.Total.Failed, op.Total.Rejected
		}
	}
	// Database failure is a process failure, unlike a saved failed delivery.
	st.err = errors.New("isolated persistence failure")
	r.onMsg(context.Background(), &nats.Msg{Data: raw}, false)
	r.onMsg(context.Background(), &nats.Msg{Data: []byte("invalid-json")}, false)
	for _, op := range reg.Snapshot().Operations {
		if op.Key != "message_handle" {
			continue
		}
		if op.Total.Succeeded != oldSuccess+1 || op.Total.Failed != oldFail+1 || op.Total.Rejected != oldReject+1 || op.Inflight != 0 {
			t.Fatalf("observation outcomes: %v", op)
		}
		if op.CompletionScope != "notification_pipeline_returned_success_not_channel_delivery_or_user_read" {
			t.Fatal(op.CompletionScope)
		}
		return
	}
	t.Fatal("notification operation missing")
}

// onMarketAlert 走 Op("market_alert_handle")：该键曾注册缺失（9a324e3 引入调用点、
// 注册表未跟随），真实触发即 panic。钉样：坏信封记 Rejected 不 panic，
// 成功链路与 channel 失败照常区分。
func TestOnMarketAlertObservation(t *testing.T) {
	st := &observationStore{}
	p := biz.NewPipeline(biz.Config{RetryMax: 1}, st, failedChannel{}, failedChannel{})
	r := NewRunner(p, nil, nil, nil, log.NewStdLogger(io.Discard))
	reg := observe.ForService("notify")
	counts := func(want bool) (uint64, uint64, bool) {
		for _, op := range reg.Snapshot().Operations {
			if op.Key == "market_alert_handle" {
				return op.Total.Succeeded, op.Total.Rejected, true
			}
		}
		return 0, 0, false
	}
	s0, rj0, ok := counts(true)
	if !ok {
		t.Fatal("market_alert_handle op 未注册（会运行时 panic）")
	}
	r.onMarketAlert(context.Background(), []byte("invalid-json"))
	_, rj1, _ := counts(true)
	if rj1 != rj0+1 {
		t.Fatalf("坏信封应记 Rejected: %d -> %d", rj0, rj1)
	}
	env, err := events.New("market", events.SubjectMarketAlert, "isolated-trace", map[string]string{"kind": "limit_up", "message": "isolated fixture"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	r.onMarketAlert(context.Background(), raw) // channel 失败但 pipeline 已落定失败状态 → 操作成功
	s1, rj2, _ := counts(true)
	if s1 != s0+1 || rj2 != rj1 {
		t.Fatalf("成功/拒绝计数不符: s %d->%d rj %d->%d", s0, s1, rj1, rj2)
	}
}
