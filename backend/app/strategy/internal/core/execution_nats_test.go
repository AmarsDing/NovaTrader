package core

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"server/app/strategy/internal/biz"
	"server/app/strategy/internal/data"
	"server/pkg/events"
)

type redeliveryProjectionRepo struct {
	biz.SignalRepo    // Other ports are intentionally unused by this focused transport fixture.
	wanted, trace     string
	attempts          atomic.Int32
	failed, succeeded chan struct{}
}

func (r *redeliveryProjectionRepo) ResolveExecutionSignal(_ context.Context, id string) (int, error) {
	if id != r.wanted {
		return 0, nil
	}
	return 1, nil
}
func (r *redeliveryProjectionRepo) ProjectExecution(ctx context.Context, id int, _ biz.ExecutionPlanner, _ biz.EnvelopeFunc) (*biz.Signal, bool, error) {
	if id != 1 || events.TraceID(ctx) != r.trace {
		return nil, false, errors.New("original transport identity lost")
	}
	attempt := r.attempts.Add(1)
	if attempt == 1 {
		close(r.failed)
		return nil, false, errors.New("synthetic SQL transaction failure")
	}
	if attempt == 2 {
		close(r.succeeded)
	}
	return &biz.Signal{ID: 1}, true, nil
}
func (*redeliveryProjectionRepo) ExecutionCandidates(context.Context, int, int) ([]int, error) {
	return nil, nil
}

func TestJetStreamExecutionProjectionFailureRedeliversOriginalIdentity(t *testing.T) {
	url := os.Getenv("NT_TEST_EXECUTION_NATS_URL")
	if url == "" {
		t.Skip("explicit isolated NT_TEST_EXECUTION_NATS_URL required")
	}
	bus, err := events.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)
	repo := &redeliveryProjectionRepo{wanted: "execution-" + uuid.NewString(), trace: "original-" + uuid.NewString(), failed: make(chan struct{}), succeeded: make(chan struct{})}
	uc := biz.NewUsecase(biz.NewSettings(nil), nil, nil, nil, repo, nil, nil, nil, nil, log.DefaultLogger)
	r := NewRunner(uc, &data.Data{}, nil, bus, nil, log.DefaultLogger)
	if err = r.subscribeExecutions(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, sub := range r.subs {
			_ = sub.Unsubscribe()
		}
		for _, name := range []string{"strategy-trade-order-v1", "strategy-trade-fill-v1"} {
			_ = bus.JetStream().DeleteConsumer(events.StreamName, name)
		}
	})
	legacy, err := events.New("trade", events.SubjectTradeOrder, "legacy-only-notification", map[string]any{"book": "paper", "symbol": "600519.SH", "count": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = bus.Publish(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	env, err := events.New("trade", events.SubjectTradeOrder, repo.trace, map[string]any{"client_order_id": repo.wanted, "signal_id": "untrusted-body-must-not-be-used", "filled": 99999})
	if err != nil {
		t.Fatal(err)
	}
	if err = bus.Publish(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	select {
	case <-repo.failed:
	case <-time.After(5 * time.Second):
		t.Fatal("first projection attempt missing")
	}
	stored, err := bus.JetStream().GetLastMsg(events.StreamName, events.SubjectTradeOrder)
	if err != nil {
		t.Fatal(err)
	}
	info, err := bus.JetStream().ConsumerInfo(events.StreamName, "strategy-trade-order-v1")
	if err != nil || info.AckFloor.Stream >= stored.Sequence {
		t.Fatalf("failed projection was acknowledged: %+v err=%v", info, err)
	}
	select {
	case <-repo.succeeded:
	case <-time.After(8 * time.Second):
		t.Fatal("original failed message was not redelivered")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, err = bus.JetStream().ConsumerInfo(events.StreamName, "strategy-trade-order-v1")
		if err == nil && info.AckFloor.Stream == stored.Sequence && info.NumAckPending == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || info.Delivered.Consumer < 2 || info.AckFloor.Stream != stored.Sequence || repo.attempts.Load() != 2 {
		t.Fatalf("missing original-ID redelivery/success acknowledgement: attempts=%d info=%+v err=%v", repo.attempts.Load(), info, err)
	}
}
