package service

import (
	"context"
	"testing"
	"time"

	"server/app/trade/internal/core"
	"server/pkg/brokerif"
)

func TestGetChannelStatus(t *testing.T) {
	// 无状态机（测试装配）时回空清单，而不是虚报通道。
	empty, err := NewTradeService(nil, nil, nil).GetChannelStatus(context.Background(), nil)
	if err != nil || len(empty.GetChannels()) != 0 {
		t.Fatalf("nil tracker must yield empty channels, got %v err %v", empty, err)
	}

	tr := brokerif.NewTracker(time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC))
	s := NewTradeService(nil, tr, nil)
	reply, err := s.GetChannelStatus(context.Background(), nil)
	if err != nil || len(reply.GetChannels()) != 1 {
		t.Fatalf("want one channel, got %v err %v", reply, err)
	}
	got := reply.GetChannels()[0]
	if got.GetChannel() != "sim" || got.GetStatus() != string(brokerif.Idle) || got.GetSince() == "" {
		t.Fatalf("initial sim channel wrong: %v", got)
	}
	when := time.Date(2026, 10, 19, 9, 31, 0, 0, time.UTC)
	if err := tr.Transition(brokerif.Connected, when); err != nil {
		t.Fatal(err)
	}
	reply, err = s.GetChannelStatus(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got = reply.GetChannels()[0]
	if got.GetStatus() != string(brokerif.Connected) || got.GetSince() != "2026-10-19T09:31:00Z" {
		t.Fatalf("after transition: %v", got)
	}
}

// emfile 服务端接线（G7）：装配了 live 通道时如实出现在读面（含起不来的诚实 down）；
// 未接线则保持缺席。通道顺序稳定（sim 在前、emfile 在后），供前端直接取索引。
func TestGetChannelStatusEmfile(t *testing.T) {
	simTime := time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)
	sim := brokerif.NewTracker(simTime)
	liveAt := time.Date(2026, 10, 20, 9, 31, 0, 0, time.UTC)
	live := brokerif.NewTracker(simTime)
	if err := live.Transition(brokerif.Down, liveAt); err != nil {
		t.Fatal(err)
	}
	s := NewTradeService(nil, sim, &core.LiveChannel{Tracker: live})
	reply, err := s.GetChannelStatus(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.GetChannels()) != 2 {
		t.Fatalf("want sim + emfile, got %v", reply.GetChannels())
	}
	em := reply.GetChannels()[1]
	if em.GetChannel() != "emfile" || em.GetStatus() != string(brokerif.Down) || em.GetSince() != "2026-10-20T09:31:00Z" {
		t.Fatalf("emfile channel wrong: %v", em)
	}
}
