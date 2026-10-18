package core

import (
	"context"
	"testing"

	platform "server/api/platform/v1"
	"server/pkg/events"
	"server/pkg/observe"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/nats-io/nats.go"
)

func rawObservation() *platform.ConsumerOperation {
	for _, op := range observe.ForService("intel").Snapshot().Operations {
		if op.Key == "raw" {
			return op
		}
	}
	return nil
}

func TestBadInputRejectsBeforeProcessWithoutClaimingAck(t *testing.T) {
	w := &Worker{log: log.NewHelper(log.DefaultLogger)}
	before := rawObservation()
	w.handle(context.Background(), &nats.Msg{Subject: "intel.raw.news", Data: []byte("broken")})
	env, _ := events.New("test", "intel.raw.news", "", []int{1, 2})
	body, _ := env.Marshal()
	w.handle(context.Background(), &nats.Msg{Subject: "intel.raw.news", Data: body})
	after := rawObservation()
	if after.Total.Attempted-before.Total.Attempted != 2 || after.Total.Rejected-before.Total.Rejected != 2 || after.Total.Succeeded != before.Total.Succeeded || after.Inflight != 0 || after.CompletionScope != "intel_process_returned_success_not_broker_ack" {
		t.Fatalf("invalid input counted as success: %v", after)
	}
}
