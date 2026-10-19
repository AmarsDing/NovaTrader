package core

import (
	"testing"
	"time"

	"server/pkg/brokerif"

	"github.com/go-kratos/kratos/v2/log"
)

// TestRunnerChannelHeartbeatWiring 验证 sim 通道心跳的接线缝：
// NewRunner 自带状态机，channelOK/channelFail 折算迁移并触发订阅（bus 为 nil 时发布只跳过不panic）。
func TestRunnerChannelHeartbeatWiring(t *testing.T) {
	r := NewRunner(nil, nil, nil, nil, nil, log.DefaultLogger)
	type event struct{ from, to brokerif.Status }
	var got []event
	r.Channel().Subscribe(func(from, to brokerif.Status, _ time.Time) { got = append(got, event{from, to}) })

	r.channelOK()
	r.channelFail()
	for i := 0; i < brokerif.FailThreshold-1; i++ {
		r.channelFail()
	}
	if s, _ := r.Channel().Current(); s != brokerif.Down {
		t.Fatalf("sim channel should be down after %d consecutive failures, got %s", brokerif.FailThreshold, s)
	}
	want := []event{{brokerif.Idle, brokerif.Connected}, {brokerif.Connected, brokerif.Degraded}, {brokerif.Degraded, brokerif.Down}}
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %v, want %v", i, got[i], want[i])
		}
	}

	// 字面量装配的 Runner（既有测试的用法）没有状态机，心跳记账必须是无操作。
	bare := &Runner{}
	bare.channelOK()
	bare.channelFail()
}
