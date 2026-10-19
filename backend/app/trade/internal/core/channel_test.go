package core

import (
	"context"
	"testing"
	"time"

	"server/pkg/brokerif"
	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
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

// TestPublishChannelOpensFreshRootTracePerTransition 钉住通道事件的链纪律（决策 C）：
// Tracker 回调结构上不带因果父，每次状态迁移即一条根链——发出的信封带合法新 uuid
// 链号、不编 causation_id，两次迁移链号互异。
func TestPublishChannelOpensFreshRootTracePerTransition(t *testing.T) {
	r := NewRunner(nil, nil, nil, nil, nil, log.DefaultLogger)
	var envs []events.Envelope
	r.publish = func(_ context.Context, env events.Envelope) error {
		envs = append(envs, env)
		return nil
	}

	r.channelOK() // Idle→Connected
	r.channelFail()
	for i := 0; i < brokerif.FailThreshold-1; i++ {
		r.channelFail()
	} // →Degraded→Down，Tracker 同步回调
	if len(envs) != 3 {
		t.Fatalf("published envs %d, want 3", len(envs))
	}
	seen := map[string]bool{}
	for _, e := range envs {
		if e.Source != "trade" || e.Subject != events.SubjectTradeChannel {
			t.Fatalf("env %+v", e)
		}
		if _, err := uuid.Parse(e.TraceID); err != nil {
			t.Fatalf("channel transition trace not a uuid: %q", e.TraceID)
		}
		if e.CausationID != "" {
			t.Fatalf("channel transition must not invent a parent: %+v", e)
		}
		if seen[e.TraceID] {
			t.Fatalf("two transitions must not share one chain: %q", e.TraceID)
		}
		seen[e.TraceID] = true
	}
}
