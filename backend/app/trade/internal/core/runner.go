package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"server/app/trade/internal/biz"
	"server/conf"
	"server/pkg/brokerif"
	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/observe"
	"server/pkg/outbox"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/google/wire"
	"github.com/nats-io/nats.go"
)

var ProviderSet = wire.NewSet(NewRunner)

// Runner 每 10 秒记一次模拟账户快照，投递 outbox，并用分钟线撮合未完成的模拟委托。
// 快照回路同时充当 sim 执行通道的心跳（G7）：连续失败按 brokerif.Observer 口径
// 折算为通道降级/停机，迁移发布到 trade.channel.status。
type Runner struct {
	now        func() time.Time
	e          *biz.Engine
	bus        *events.Bus
	box        *outbox.Dispatcher
	track      *brokerif.Tracker
	obs        *brokerif.Observer
	interval   time.Duration
	log        *log.Helper
	sub        *nats.Subscription
	exitSub    *nats.Subscription
	cancel     context.CancelFunc
	watchOwner string
	lifecycle  sync.Mutex
	runCtx     context.Context
	started    bool
	stopping   bool
	done       chan struct{}
	wg         sync.WaitGroup
}

func NewRunner(c *conf.Trade, e *biz.Engine, bus *events.Bus, box *outbox.Dispatcher, logger log.Logger) *Runner {
	every := 10 * time.Second
	if c != nil && c.GetSnapshotInterval() != nil && c.GetSnapshotInterval().AsDuration() > 0 {
		every = c.GetSnapshotInterval().AsDuration()
	}
	r := &Runner{e: e, bus: bus, box: box, interval: every, log: log.NewHelper(logger), watchOwner: "trade." + uuid.NewString()}
	r.track = brokerif.NewTracker(time.Now())
	r.obs = brokerif.NewObserver(r.track, time.Now)
	r.track.Subscribe(r.publishChannel)
	return r
}

// Channel 暴露 sim 执行通道的状态机，供状态读面与测试订阅（G7）。
func (r *Runner) Channel() *brokerif.Tracker { return r.track }

// channelOK/channelFail 是 nil 安全的心跳记账；测试里按字面量拼的 Runner 没有状态机。
func (r *Runner) channelOK() {
	if r.obs != nil {
		r.obs.Ok()
	}
}

func (r *Runner) channelFail() {
	if r.obs != nil {
		r.obs.Fail()
	}
}

// publishChannel 把每次状态迁移投递到 trade.channel.status（随 trade.> 走 JetStream）。
// 回调由 Tracker 在锁外同步触发，发布带 5s 上限，不会反压状态机。
func (r *Runner) publishChannel(from, to brokerif.Status, at time.Time) {
	if r.bus == nil {
		return
	}
	env, err := events.New("trade", events.SubjectTradeChannel, "", map[string]any{
		"channel": "sim", "from": string(from), "to": string(to),
		"at": at.UTC().Format(time.RFC3339),
	})
	if err != nil {
		r.log.Warnf("channel event: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.bus.Publish(ctx, env); err != nil {
		r.log.Warnf("publish channel %s→%s: %v", from, to, err)
	}
}

func (r *Runner) Start(ctx context.Context) error {
	r.lifecycle.Lock()
	if r.stopping {
		r.lifecycle.Unlock()
		return fmt.Errorf("trade runner is stopped; create a new runner to restart")
	}
	if r.started {
		r.lifecycle.Unlock()
		return nil
	}
	r.started = true
	r.runCtx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})
	ctx = r.runCtx
	// Stop also waits for startup, so it cannot miss a subscription being installed.
	r.wg.Add(1)
	r.lifecycle.Unlock()
	defer r.wg.Done()
	startupCtx, startupCancel := context.WithTimeout(ctx, 10*time.Second)
	defer startupCancel()
	if err := observedTradeCall("snapshot", func() error { _, err := r.e.Snapshot(startupCtx, "SIM"); return err }); err != nil {
		r.beginStop()
		return err
	}
	if err := observedTradeCall("intent_recovery", func() error { return r.e.RecoverIntents(startupCtx) }); err != nil && startupCtx.Err() == nil {
		r.log.Warnf("recover intents: %v", err)
	}
	if err := startupCtx.Err(); err != nil {
		r.beginStop()
		return err
	}
	sub, err := r.bus.Conn().Subscribe(events.SubjectMarketBar, r.onBars)
	if err != nil {
		r.log.Warnf("subscribe bars: %v", err)
	} else {
		r.keepSubscription(sub, false)
	}
	if sub, err = r.subscribeExit(startupCtx); err != nil {
		r.log.Warnf("subscribe exit: %v", err)
	} else {
		r.keepSubscription(sub, true)
	}
	r.lifecycle.Lock()
	if r.stopping || ctx.Err() != nil {
		r.lifecycle.Unlock()
		r.beginStop()
		return context.Canceled
	}
	for _, run := range []func(context.Context){r.loop, r.evidenceLoop, r.watchLoop} {
		r.wg.Add(1)
		go func(run func(context.Context)) {
			defer r.wg.Done()
			if ctx.Err() == nil {
				run(ctx)
			}
		}(run)
	}
	r.lifecycle.Unlock()
	// 引擎装配（首帧快照）已是 Start 前置条件；走到这里通道视为连上。
	r.channelOK()
	return nil
}

const exitConsumer = "trade_risk_exit"

func (r *Runner) subscribeExit(ctx context.Context) (*nats.Subscription, error) {
	js := r.bus.JetStream()
	info, err := js.ConsumerInfo(events.StreamName, exitConsumer, nats.Context(ctx))
	if errors.Is(err, nats.ErrConsumerNotFound) {
		info, err = js.AddConsumer(events.StreamName, &nats.ConsumerConfig{
			Durable: exitConsumer, FilterSubject: events.SubjectRiskExit, DeliverSubject: nats.NewInbox(),
			DeliverPolicy: nats.DeliverAllPolicy, AckPolicy: nats.AckExplicitPolicy,
			AckWait: 30 * time.Second, MaxAckPending: 64,
		}, nats.Context(ctx))
	}
	if err != nil {
		return nil, err
	}
	if info.Config.FilterSubject != events.SubjectRiskExit || info.Config.AckPolicy != nats.AckExplicitPolicy || info.Config.DeliverSubject == "" || info.Config.DeliverGroup != "" {
		return nil, fmt.Errorf("risk exit durable has incompatible configuration")
	}
	// Explicitly bind a durable owned by the server. Unsubscribe on shutdown then
	// stops callbacks without deleting its acknowledgement/recovery position.
	return js.Subscribe(events.SubjectRiskExit, r.onExit, nats.Bind(events.StreamName, exitConsumer), nats.ManualAck())
}

func (r *Runner) keepSubscription(sub *nats.Subscription, exit bool) {
	r.lifecycle.Lock()
	if !r.stopping && r.runCtx.Err() == nil {
		if exit {
			r.exitSub = sub
		} else {
			r.sub = sub
		}
		r.lifecycle.Unlock()
		return
	}
	r.lifecycle.Unlock()
	_ = sub.Unsubscribe()
}

// beginCallback and beginStop serialize WaitGroup admission. Once stopping is
// visible, even a queued callback cannot begin new engine work.
func (r *Runner) beginCallback() (context.Context, func(), bool) {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if r.stopping {
		return nil, nil, false
	}
	base := r.runCtx
	if base == nil { // Existing isolated core tests call handlers without Start.
		base = context.Background()
	}
	if base.Err() != nil {
		return nil, nil, false
	}
	r.wg.Add(1)
	ctx, cancel := context.WithTimeout(base, 10*time.Second)
	return ctx, func() { cancel(); r.wg.Done() }, true
}

// Evidence collection has its own bounded loop. A slow source or evidence DB
// cannot stall the order/outbox ticker, and no client-supplied clock is used.
func (r *Runner) evidenceLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	sample := func() {
		if ctx.Err() != nil {
			return
		}
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		finish := observe.ForService("trade").Op("paper_evidence").Begin()
		stored, err := r.e.RecordPaperRunSampleObserved(c)
		if err == nil && !stored {
			finish(observe.Skipped)
		} else {
			observe.FinishError(finish, err)
		}
		if err != nil && ctx.Err() == nil {
			r.log.Warnf("paper run evidence: %v", err)
		}
	}
	sample()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample()
		}
	}
}

// onBars 解开 M02 的事件信封，再把分钟线交给模拟撮合；不接受无来源的裸载荷。
func (r *Runner) onBars(msg *nats.Msg) {
	notice := observe.ForService("trade").Op("minute_notice")
	notice.Received()
	noticeFinish := notice.Begin()
	noticeOutcome := observe.Rejected
	defer func() { noticeFinish(noticeOutcome) }()
	ctx, finish, active := r.beginCallback()
	if !active {
		noticeOutcome = observe.Cancelled
		return
	}
	defer finish()
	env, err := events.Unmarshal(msg.Data)
	if err != nil || env.Subject != events.SubjectMarketBar || env.Source != "market" {
		r.log.Warnf("bar envelope: subject=%s error=%v", env.Subject, err)
		return
	}
	var body struct {
		Bars []market.Bar `json:"bars"`
	}
	if err := json.Unmarshal(env.Payload, &body); err != nil {
		r.log.Warnf("bar payload: %v", err)
		return
	}
	if len(body.Bars) > market.MaxTradeWatchSymbols {
		return
	}
	noticeOutcome = observe.Succeeded
	if len(body.Bars) == 0 {
		noticeOutcome = observe.Skipped
	}
	for _, b := range body.Bars {
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		if r.now != nil {
			now = r.now()
		}
		op := observe.ForService("trade").Op("minute_bar")
		op.Received()
		done := op.Begin()
		if b.Time.IsZero() || now.Before(b.Time.Add(time.Minute)) || now.Sub(b.Time) > 2*time.Minute {
			done(observe.Rejected)
			continue
		}
		up, down, known := b.ExecutionPrices()
		if !known {
			done(observe.Rejected)
			continue
		}
		applied, err := r.e.ApplySourceBar(events.ContextWithEnvelope(ctx, env), "SIM", b.Symbol, biz.Quote{
			Time: b.Time,
			Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume,
			LimitUp: up, LimitDown: down,
		})
		if err != nil {
			observe.FinishError(done, err)
		} else if !applied.Consumed {
			done(observe.Skipped)
		} else {
			done(observe.Succeeded)
		}
		if err != nil {
			r.log.Warnf("apply %s: %v", b.Symbol, err)
		}
	}
}

func (r *Runner) onExit(msg *nats.Msg) {
	op := observe.ForService("trade").Op("risk_exit")
	op.Received()
	observed := op.Begin()
	outcome := observe.Rejected
	defer func() { observed(outcome) }()
	ctx, finish, active := r.beginCallback()
	if !active {
		outcome = observe.Cancelled
		return // Leave unacknowledged for the next active instance.
	}
	defer finish()
	env, err := events.Unmarshal(msg.Data)
	if err != nil || env.Subject != events.SubjectRiskExit || env.Source != "risk" || msg.Subject != events.SubjectRiskExit {
		r.log.Warnf("exit envelope: %v", err)
		_ = msg.Ack()
		return
	}
	var body struct {
		ExitID  string  `json:"exit_id"`
		Symbol  string  `json:"symbol"`
		Account string  `json:"account_type"`
		Volume  int     `json:"volume"`
		Price   float64 `json:"price"`
		Trigger string  `json:"trigger"`
	}
	if err := json.Unmarshal(env.Payload, &body); err != nil || body.ExitID == "" || body.Volume <= 0 || body.Price <= 0 || (body.Account != "SIM" && body.Account != "LIVE") {
		r.log.Warnf("exit payload: %v", err)
		_ = msg.Ack()
		return
	}
	if ctx.Err() != nil {
		outcome = observe.Cancelled
		return
	}
	order, err := r.e.Place(events.ContextWithEnvelope(ctx, env), biz.PlaceRequest{
		ClientOrderID: body.ExitID,
		Account:       body.Account,
		Symbol:        body.Symbol,
		Side:          "sell",
		Price:         body.Price,
		Volume:        body.Volume,
		Source:        "watch",
		Operator:      "risk",
		// A protection trigger such as "stop_loss" is not a strategy signal ID.
		// Its original event remains the causal source of this exit order.
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = observe.Cancelled
		} else {
			outcome = observe.Failed
		}
		r.log.Warnf("exit place %s: %v", body.Symbol, err)
		if ctx.Err() == nil {
			_ = msg.Nak()
		}
		return
	}
	if order.Status == biz.StatusRejected {
		outcome = observe.Rejected
	} else {
		outcome = observe.Succeeded
	}
	r.log.Infof("exit order %s %s %s", order.ClientOrderID, order.Status, order.Reason)
	_ = msg.Ack()
}

func (r *Runner) beginStop() <-chan struct{} {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if r.done == nil {
		r.done = make(chan struct{})
	}
	if !r.stopping {
		r.stopping = true
		if r.cancel != nil {
			r.cancel()
		}
		bars, exits := r.sub, r.exitSub
		r.sub, r.exitSub = nil, nil
		go func() {
			if bars != nil {
				_ = bars.Unsubscribe()
			}
			if exits != nil {
				_ = exits.Unsubscribe()
			}
			r.wg.Wait()
			close(r.done)
		}()
	}
	return r.done
}

func (r *Runner) Stop(ctx context.Context) error {
	done := r.beginStop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runner) loop(ctx context.Context) {
	snap := time.NewTicker(r.interval)
	dispatch := time.NewTicker(500 * time.Millisecond)
	defer snap.Stop()
	defer dispatch.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-snap.C:
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			if c.Err() != nil {
				cancel()
				return
			}
			if err := observedTradeCall("intent_recovery", func() error { return r.e.RecoverIntents(c) }); err != nil && c.Err() == nil {
				r.log.Warnf("recover intents: %v", err)
			}
			if c.Err() == nil {
				if err := observedTradeCall("snapshot", func() error { _, err := r.e.Snapshot(c, "SIM"); return err }); err != nil && c.Err() == nil {
					r.log.Warnf("snapshot: %v", err)
					r.channelFail()
				} else if c.Err() == nil {
					r.channelOK()
				}
			}
			cancel()
		case <-dispatch.C:
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			if c.Err() != nil {
				cancel()
				return
			}
			if err := observedTradeCall("plan_tick", func() error { return r.e.TickPlans(c) }); err != nil && c.Err() == nil {
				r.log.Debugf("conditional plans: %v", err)
			}
			if c.Err() == nil {
				if err := observedTradeCall("confirm_expiry", func() error { return r.e.ExpireConfirms(c) }); err != nil && c.Err() == nil {
					r.log.Warnf("confirm: %v", err)
				}
			}
			if c.Err() == nil {
				if err := observedTradeCall("watch_sweep", func() error { return r.e.SweepWorking(c) }); err != nil && c.Err() == nil {
					r.log.Warnf("chase: %v", err)
				}
			}
			if c.Err() == nil {
				if err := observedTradeCall("outbox", func() error { _, err := r.box.Dispatch(c, 100); return err }); err != nil && c.Err() == nil {
					r.log.Warnf("outbox: %v", err)
				}
			}
			cancel()
		}
	}
}
