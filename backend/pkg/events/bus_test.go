package events

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestMissingSubjects(t *testing.T) {
	got := missingSubjects([]string{"sys.>", "trade.>"}, []string{"sys.>", "trade.>", "intel.>"})
	if len(got) != 1 || got[0] != "intel.>" {
		t.Fatalf("missing = %v", got)
	}
	if got := missingSubjects(StreamSubjects(), StreamSubjects()); len(got) != 0 {
		t.Fatalf("missing = %v", got)
	}
}

func TestStreamCapturesIntel(t *testing.T) {
	bus, err := Dial("")
	if err != nil {
		t.Skip(err)
	}
	defer bus.Close()
	info, err := bus.JetStream().StreamInfo(StreamName)
	if err != nil {
		t.Fatal(err)
	}
	if got := missingSubjects(info.Config.Subjects, StreamSubjects()); len(got) != 0 {
		t.Fatalf("stream still lacks %v", got)
	}
}

func TestBusPersistsOrderAndSkipsMarket(t *testing.T) {
	bus, err := Dial("")
	if err != nil {
		t.Skip(err)
	}
	defer bus.Close()

	nc, err := nats.Connect(nats.DefaultURL, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	orderSub, err := js.SubscribeSync(SubjectTradeOrder, nats.DeliverNew())
	if err != nil {
		t.Fatal(err)
	}
	defer orderSub.Unsubscribe()
	marketSub, err := nc.SubscribeSync(SubjectMarketSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer marketSub.Unsubscribe()
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	order, err := New("trade", SubjectTradeOrder, "trace-js", map[string]string{"symbol": "600519.SH"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := bus.Publish(ctx, order); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, order); err != nil {
		t.Fatal(err)
	}
	msg, err := orderSub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(msg.Data)
	if err != nil {
		t.Fatal(err)
	}
	if got.EventID != order.EventID {
		t.Fatalf("event_id = %s", got.EventID)
	}
	if _, err := orderSub.NextMsg(300 * time.Millisecond); err == nil {
		t.Fatal("duplicate event_id was stored again")
	}

	snap, err := New("datahub", SubjectMarketSnapshot, "", map[string]int{"px": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := marketSub.NextMsg(2 * time.Second); err != nil {
		t.Fatal(err)
	}
}
