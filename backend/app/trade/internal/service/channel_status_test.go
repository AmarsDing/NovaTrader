package service

import (
	"context"
	"testing"
	"time"

	"server/pkg/brokerif"
)

func TestGetChannelStatus(t *testing.T) {
	// 无状态机（测试装配）时回空清单，而不是虚报通道。
	empty, err := NewTradeService(nil, nil).GetChannelStatus(context.Background(), nil)
	if err != nil || len(empty.GetChannels()) != 0 {
		t.Fatalf("nil tracker must yield empty channels, got %v err %v", empty, err)
	}

	tr := brokerif.NewTracker(time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC))
	s := NewTradeService(nil, tr)
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
