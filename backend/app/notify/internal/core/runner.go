// Package core 订阅交易、风控、成交和报告事件，交给通知流水线。
package core

import (
	"context"
	"os"
	"sync"
	"time"

	"server/app/notify/internal/biz"
	"server/conf"
	"server/pkg/events"
	"server/pkg/observe"
	"server/pkg/registry"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/nats-io/nats.go"
)

var ProviderSet = wire.NewSet(NewRunner)

const (
	flushEvery = 15 * time.Second
	ackWait    = 60 * time.Second
	beatEvery  = 15 * time.Second
)

// Runner 是常驻消费者。JetStream 订阅在停止时不退订，退订会删掉持久消费者。
type Runner struct {
	p      *biz.Pipeline
	beater *registry.Beater
	bus    *events.Bus
	log    *log.Helper
	addr   string
	id     string
	cancel context.CancelFunc
	wg     sync.WaitGroup
	subs   []*nats.Subscription // 普通 NATS 订阅（market.alert），Stop 时退订
}

func NewRunner(p *biz.Pipeline, beat *registry.Beater, bus *events.Bus, cfg *conf.Notify, logger log.Logger) *Runner {
	id, _ := os.Hostname()
	addr := ":2101" // 端口默认值，configs/config.yaml notify.http.addr 可覆盖
	if h := cfg.GetHttp(); h != nil && h.GetAddr() != "" {
		addr = h.GetAddr()
	}
	return &Runner{p: p, beater: beat, bus: bus, log: log.NewHelper(log.With(logger, "module", "notify/core")), addr: addr, id: id}
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
	// market.alert 走普通 NATS（market.* 不进 JetStream，见 events.Persistent；T07③）。
	// 无 ack 语义：失败只记观察与日志，引擎侧 5 分钟重发窗让下一次异动自然补达。
	if r.bus.Conn() != nil {
		sub, err := r.bus.Conn().Subscribe(events.SubjectMarketAlert, func(m *nats.Msg) { r.onMarketAlert(runCtx, m.Data) })
		if err != nil {
			cancel()
			return err
		}
		r.subs = append(r.subs, sub)
	}
	r.wg.Add(2)
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
	go r.beatLoop(runCtx)
	r.log.Info("notify core started")
	return nil
}

func (r *Runner) Stop(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	for _, sub := range r.subs {
		_ = sub.Unsubscribe()
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
		// Flush 的触发事件只是重投指令，不是被重投消息的因果父——父信封不下放；
		// 各行链号仍以行内携带的 trace（DesktopNote.TraceID）为准，不编因果。
		handleErr = r.p.Flush(ctx)
	} else {
		// 直发消费：父信封完整下传，notify.desktop 才能经发射缝续上真实因果父。
		handleErr = r.p.Handle(events.ContextWithEnvelope(ctx, env), env)
	}
	observe.FinishError(finish, handleErr)
	if handleErr != nil {
		r.log.Errorf("%s %s: %v", env.Subject, env.EventID, handleErr)
		_ = m.Nak()
		return
	}
	_ = m.Ack()
}

// onMarketAlert 处理普通 NATS 上的 market.alert（T07③）：market.* 不进 JetStream，
// 没有持久消费者与 ack；处理失败只记观测，由引擎侧 5 分钟重发窗与 next alert 自然补达。
func (r *Runner) onMarketAlert(ctx context.Context, body []byte) {
	op := observe.ForService("notify").Op("market_alert_handle")
	op.Received()
	finish := op.Begin()
	env, err := events.Unmarshal(body)
	if err != nil {
		finish(observe.Rejected)
		r.log.Warnf("bad market.alert envelope: %v", err)
		return
	}
	ctx = events.WithTraceID(ctx, env.TraceID)
	// 父信封完整下放，notify.desktop 沿真实因果父续链，与 JetStream 直发链同一缝合规则。
	handleErr := r.p.Handle(events.ContextWithEnvelope(ctx, env), env)
	observe.FinishError(finish, handleErr)
	if handleErr != nil {
		r.log.Errorf("%s %s: %v", env.Subject, env.EventID, handleErr)
	}
}

// A successful flush means the pipeline returned nil; individual channel results
// remain in notification records and are not inferred from this process metric.
func (r *Runner) flush(ctx context.Context) error {
	finish := observe.ForService("notify").Op("held_flush").Begin()
	err := r.p.Flush(ctx)
	observe.FinishError(finish, err)
	return err
}

func (r *Runner) beat(ctx context.Context) {
	if err := r.beater.Beat(ctx, "notify", r.id, r.addr); err != nil && ctx.Err() == nil {
		r.log.Errorf("heartbeat: %v", err)
	}
}

func (r *Runner) beatLoop(ctx context.Context) {
	defer r.wg.Done()
	if r.beater == nil {
		return // 测试装配允许 nil beater
	}
	t := time.NewTicker(beatEvery)
	defer t.Stop()
	r.beat(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.beat(ctx)
		}
	}
}
