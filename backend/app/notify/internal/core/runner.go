// Package core 订阅交易、风控、成交和报告事件，交给通知流水线。
package core

import (
	"context"
	"sync"
	"time"

	"server/app/notify/internal/biz"
	"server/pkg/events"
	"server/pkg/observe"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/nats-io/nats.go"
)

var ProviderSet = wire.NewSet(NewRunner)

const (
	flushEvery = 15 * time.Second
	ackWait    = 60 * time.Second
)

// Runner 是常驻消费者。JetStream 订阅在停止时不退订，退订会删掉持久消费者。
type Runner struct {
	p      *biz.Pipeline
	bus    *events.Bus
	log    *log.Helper
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewRunner(p *biz.Pipeline, bus *events.Bus, logger log.Logger) *Runner {
	return &Runner{p: p, bus: bus, log: log.NewHelper(log.With(logger, "module", "notify/core"))}
}

func (r *Runner) Start(ctx context.Context) error {
	finish := observe.ForService("notify").Op("recover").Begin()
	err := r.p.Recover(ctx)
	observe.FinishError(finish, err)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	subjects := []struct {
		subject  string
		consumer string
		flush    bool
	}{
		{events.SubjectNotifyRequest, "notify-request", false},
		{events.SubjectSignal, "notify-signal", false},
		{events.SubjectRiskAlert, "notify-risk", false},
		{events.SubjectIntelAlert, "notify-intel", false},
		{events.SubjectTradeFill, "notify-fill", false},
		{events.SubjectBriefing, "notify-briefing", false},
		{events.SubjectStrategyReview, "notify-review", false},
		{events.SubjectSessionChanged, "notify-session", true},
	}
	js := r.bus.JetStream()
	for _, s := range subjects {
		s := s
		if _, err := js.Subscribe(s.subject, func(m *nats.Msg) { r.onMsg(runCtx, m, s.flush) },
			nats.Durable(s.consumer), nats.ManualAck(), nats.DeliverNew(),
			nats.AckWait(ackWait), nats.MaxDeliver(20),
		); err != nil {
			cancel()
			return err
		}
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(flushEvery)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				if err := r.flush(runCtx); err != nil {
					r.log.Errorf("flush: %v", err)
				}
			}
		}
	}()
	r.log.Info("notify core started")
	return nil
}

func (r *Runner) Stop(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *Runner) onMsg(ctx context.Context, m *nats.Msg, flush bool) {
	key := "message_handle"
	if flush {
		key = "held_flush"
	}
	op := observe.ForService("notify").Op(key)
	op.Received()
	finish := op.Begin()
	env, err := events.Unmarshal(m.Data)
	if err != nil {
		finish(observe.Rejected)
		r.log.Warnf("bad envelope: %v", err)
		_ = m.Ack()
		return
	}
	ctx = events.WithTraceID(ctx, env.TraceID)
	var handleErr error
	if flush {
		handleErr = r.p.Flush(ctx)
	} else {
		handleErr = r.p.Handle(ctx, env)
	}
	observe.FinishError(finish, handleErr)
	if handleErr != nil {
		r.log.Errorf("%s %s: %v", env.Subject, env.EventID, handleErr)
		_ = m.Nak()
		return
	}
	_ = m.Ack()
}

// A successful flush means the pipeline returned nil; individual channel results
// remain in notification records and are not inferred from this process metric.
func (r *Runner) flush(ctx context.Context) error {
	finish := observe.ForService("notify").Op("held_flush").Begin()
	err := r.p.Flush(ctx)
	observe.FinishError(finish, err)
	return err
}
