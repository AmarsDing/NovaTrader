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
