// Package core 是 risk 的常驻部分：订阅账户、行情、情报和 Kill Switch 请求，跑每秒的检查和 outbox 投递。
package core

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"server/app/risk/internal/biz"
	"server/app/risk/internal/data"
	"server/pkg/events"
	"server/pkg/outbox"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/nats-io/nats.go"
)

var ProviderSet = wire.NewSet(NewRunner)

const (
	tickEvery     = time.Second
	pollEvery     = 3 * time.Second
	dispatchEvery = 500 * time.Millisecond

	consumerKill  = "risk-killswitch"
	consumerIntel = "risk-intel-alert"
)

// Runner 实现 kratos transport.Server。
type Runner struct {
	e     *biz.Engine
	bus   *events.Bus
	box   *outbox.Dispatcher
	audit *data.Auditor
	log   *log.Helper
	seen  *events.Seen

	lastSnap atomic.Int64

	cancel      context.CancelFunc
	auditCancel context.CancelFunc
	wg          sync.WaitGroup
	subs        []*nats.Subscription
}

func NewRunner(e *biz.Engine, bus *events.Bus, box *outbox.Dispatcher, audit *data.Auditor, logger log.Logger) *Runner {
	return &Runner{
		e: e, bus: bus, box: box, audit: audit,
		log:  log.NewHelper(log.With(logger, "module", "risk/core")),
		seen: events.NewSeen(4096),
	}
}

func (r *Runner) Start(ctx context.Context) error {
	auditCtx, auditCancel := context.WithCancel(context.Background())
	r.auditCancel = auditCancel
	go r.audit.Run(auditCtx)

	if err := r.e.Start(ctx); err != nil {
		auditCancel()
		return err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	nc := r.bus.Conn()
	core := []struct {
		subject string
		fn      func(context.Context, events.Envelope)
	}{
		{events.SubjectTradeAccount, r.onAccount},
		{events.SubjectTradeOrder, r.onOrder},
		{events.SubjectMarketSnapshot, r.onSnapshot},
		{events.SubjectMarketState, r.onState},
	}
	for _, c := range core {
		fn := c.fn
		sub, err := nc.Subscribe(c.subject, func(m *nats.Msg) {
			env, ok := r.envelope(m.Data)
			if ok {
				fn(events.WithTraceID(runCtx, env.TraceID), env)
			}
		})
		if err != nil {
			r.stop()
			return err
		}
		r.subs = append(r.subs, sub)
	}
	js := r.bus.JetStream()
	if _, err := js.Subscribe(events.SubjectKillSwitchRequest, func(m *nats.Msg) { r.onKillRequest(runCtx, m) },
		nats.Durable(consumerKill), nats.ManualAck(), nats.DeliverNew(),
		nats.AckWait(10*time.Second), nats.MaxDeliver(100),
	); err != nil {
		r.stop()
		return err
	}
	if _, err := js.Subscribe(events.SubjectIntelAlert, func(m *nats.Msg) { r.onIntelAlert(runCtx, m) },
		nats.Durable(consumerIntel), nats.ManualAck(), nats.DeliverNew(),
		nats.AckWait(10*time.Second), nats.MaxDeliver(5),
	); err != nil {
		r.stop()
		return err
	}

	r.loop(runCtx, tickEvery, r.e.Tick)
	r.loop(runCtx, pollEvery, r.poll)
	r.loop(runCtx, dispatchEvery, r.dispatch)
	r.log.Infof("risk core started")
	return nil
}

// Stop 先停订阅和循环，再让审计把队列写完。JetStream 订阅不退订，退订会删掉持久消费者。
func (r *Runner) Stop(context.Context) error {
	r.stop()
	if r.auditCancel != nil {
		r.auditCancel()
		select {
		case <-r.audit.Done():
		case <-time.After(15 * time.Second):
			r.log.Warn("audit flush timed out")
		}
	}
	return nil
}

func (r *Runner) stop() {
	for _, s := range r.subs {
		_ = s.Unsubscribe()
	}
	r.subs = nil
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

func (r *Runner) loop(ctx context.Context, every time.Duration, fn func(context.Context)) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fn(ctx)
			}
		}
	}()
}

func (r *Runner) envelope(b []byte) (events.Envelope, bool) {
	env, err := events.Unmarshal(b)
	if err != nil {
		r.log.Warnf("bad envelope: %v", err)
		return env, false
	}
	return env, r.seen.First(env.EventID)
}

func decode[T any](r *Runner, env events.Envelope) (T, bool) {
	var v T
	if err := json.Unmarshal(env.Payload, &v); err != nil {
		r.log.Warnf("bad %s payload %s: %v", env.Subject, env.EventID, err)
		return v, false
	}
	return v, true
}

func (r *Runner) onAccount(ctx context.Context, env events.Envelope) {
	if p, ok := decode[biz.AccountPayload](r, env); ok {
		r.e.OnAccount(ctx, p)
	}
}

func (r *Runner) onOrder(_ context.Context, env events.Envelope) {
	if p, ok := decode[biz.OrderActionPayload](r, env); ok {
		r.e.OnOrderAction(p)
	}
}

func (r *Runner) onSnapshot(ctx context.Context, env events.Envelope) {
	if p, ok := decode[biz.SnapshotMetaPayload](r, env); ok {
		r.lastSnap.Store(time.Now().UnixNano())
		r.e.OnSnapshot(ctx, p)
	}
}

func (r *Runner) onState(ctx context.Context, env events.Envelope) {
	if p, ok := decode[biz.StatePayload](r, env); ok {
		r.e.OnMarketState(ctx, p)
	}
}

// poll 在 market.snapshot 断流时按间隔读持仓行情，止损不依赖事件。
func (r *Runner) poll(ctx context.Context) {
	if time.Since(time.Unix(0, r.lastSnap.Load())) < pollEvery {
		return
	}
	r.e.OnSnapshot(ctx, biz.SnapshotMetaPayload{})
}

func (r *Runner) dispatch(ctx context.Context) {
	if _, err := r.box.Dispatch(ctx, 100); err != nil && ctx.Err() == nil {
		r.log.Warnf("outbox dispatch: %v", err)
	}
}

func (r *Runner) onKillRequest(ctx context.Context, m *nats.Msg) {
	if ctx.Err() != nil {
		_ = m.Nak()
		return
	}
	env, err := events.Unmarshal(m.Data)
	if err != nil {
		r.log.Errorf("bad kill request envelope: %v", err)
		_ = m.Term()
		return
	}
	p, ok := decode[biz.KillRequestPayload](r, env)
	if !ok {
		_ = m.Term()
		return
	}
	if p.Source == "" {
		p.Source = env.Source
	}
	st, err := r.e.TriggerKill(events.WithTraceID(ctx, env.TraceID), p.Source, p.Reason, "")
	switch {
	case err == nil:
		_ = m.Ack()
	case st.Kill.Active:
		// 内存已触发，落库失败重投，保证重启后还能恢复。
		r.log.Errorf("kill request %s not saved: %v", env.EventID, err)
		_ = m.NakWithDelay(time.Second)
	default:
		r.log.Errorf("kill request %s rejected: %v", env.EventID, err)
		_ = m.Term()
	}
}

// onIntelAlert 只认当天的消息，重启后补收的旧消息不算利空。
func (r *Runner) onIntelAlert(ctx context.Context, m *nats.Msg) {
	if ctx.Err() != nil {
		_ = m.Nak()
		return
	}
	defer func() { _ = m.Ack() }()
	env, err := events.Unmarshal(m.Data)
	if err != nil {
		r.log.Warnf("bad intel alert envelope: %v", err)
		return
	}
	sh := tradecal.Shanghai()
	if env.Time.In(sh).Format("2006-01-02") != time.Now().In(sh).Format("2006-01-02") {
		return
	}
	if p, ok := decode[biz.IntelAlertPayload](r, env); ok {
		r.e.OnIntelAlert(p)
	}
}
