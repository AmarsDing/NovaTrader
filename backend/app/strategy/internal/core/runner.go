// Package core 是 strategy 的常驻逻辑：按时段扫描、卖出检查、过期、后验回填、outbox 投递，以及事件订阅。
package core

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"server/app/strategy/internal/biz"
	"server/app/strategy/internal/data"
	"server/conf"
	"server/pkg/events"
	"server/pkg/outbox"
	"server/pkg/scheduler"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/nats-io/nats.go"
)

var ProviderSet = wire.NewSet(NewBus, NewRunner)

const (
	tick          = time.Second
	expireEvery   = 30 * time.Second
	schedEvery    = 30 * time.Second
	alertDebounce = 2 * time.Second
	jobPremarket  = "m06-premarket-scan"
	jobOutcomes   = "m06-outcomes"
)

// NewBus 连接 NATS。连不上时返回 nil：服务照常扫描，事件留在 outbox 等下次投递。
func NewBus(c *conf.Nats, logger log.Logger) (*events.Bus, func()) {
	bus, err := events.Dial(c.GetUrl())
	if err != nil {
		log.NewHelper(logger).Warnf("strategy: nats unavailable, events stay in outbox: %v", err)
		return nil, func() {}
	}
	return bus, bus.Close
}

// Runner 实现 kratos 的 transport.Server，随服务启停。
type Runner struct {
	uc    *biz.Usecase
	d     *data.Data
	bus   *events.Bus
	cal   *tradecal.Calendar
	sched *scheduler.Scheduler
	log   *log.Helper
	now   func() time.Time

	mu       sync.Mutex
	alerted  map[string]struct{}
	alertAt  time.Time
	lastScan time.Time
	lastFull time.Time
	lastExp  time.Time
	lastSch  time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
	subs   []*nats.Subscription
	seen   *events.Seen
}

func NewRunner(uc *biz.Usecase, d *data.Data, bus *events.Bus, logger log.Logger) *Runner {
	r := &Runner{
		uc: uc, d: d, bus: bus, cal: tradecal.Default,
		log: log.NewHelper(log.With(logger, "module", "strategy/core")),
		now: time.Now, alerted: map[string]struct{}{}, seen: events.NewSeen(10000),
	}
	r.sched = scheduler.New(r.cal, scheduler.EntStore{Client: d.Client},
		scheduler.Job{
			Name: jobPremarket, Spec: "0 9 * * *", TradingDayOnly: true,
			Timeout: 3 * time.Minute, Retry: 1, MaxDelay: 25 * time.Minute,
			Run: func(ctx context.Context) error {
				rep, err := uc.Scan(ctx, biz.ScanRequest{Pool: biz.PoolPre, Full: true, AsOf: r.now()})
				r.report(rep, err)
				return err
			},
		},
		scheduler.Job{
			Name: jobOutcomes, Spec: "40 15 * * *", TradingDayOnly: true,
			Timeout: 5 * time.Minute, Retry: 1, MaxDelay: 6 * time.Hour,
			Run: func(ctx context.Context) error {
				n, err := uc.FillOutcomes(ctx, r.now())
				r.log.Infof("outcomes filled=%d err=%v", n, err)
				return err
			},
		},
	)
	return r
}

func (r *Runner) Start(ctx context.Context) error {
	if err := r.subscribe(); err != nil {
		r.log.Warnf("subscribe: %v", err)
	}
	loopCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Add(2)
	go r.loop(loopCtx, r.housekeep)
	go r.loop(loopCtx, r.step)
	return nil
}

func (r *Runner) Stop(ctx context.Context) error {
	for _, s := range r.subs {
		_ = s.Unsubscribe()
	}
	if r.cancel == nil {
		return nil
	}
	r.cancel()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return nil
}

func (r *Runner) loop(ctx context.Context, fn func(context.Context, time.Time)) {
	defer r.wg.Done()
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx, r.now())
		}
	}
}

// housekeep 投递 outbox、过期信号。单独一个协程，扫描再慢也不耽误信号送到风控。
func (r *Runner) housekeep(ctx context.Context, now time.Time) {
	if r.bus != nil {
		if _, err := outbox.Dispatch(ctx, r.d.Client, r.bus, 200); err != nil {
			r.log.Warnf("outbox dispatch: %v", err)
		}
	}
	if now.Sub(r.lastExp) >= expireEvery {
		r.lastExp = now
		if n, err := r.uc.ExpireDue(ctx, now); err != nil {
			r.log.Errorf("expire: %v", err)
		} else if n > 0 {
			r.log.Infof("expired %d signals", n)
		}
	}
}

// step 是扫描调度，一秒一次。各项任务按自己的间隔执行，出错只记日志，不中断循环。
func (r *Runner) step(ctx context.Context, now time.Time) {
	if now.Sub(r.lastSch) >= schedEvery {
		r.lastSch = now
		if err := r.sched.Tick(ctx, now); err != nil {
			r.log.Errorf("scheduler: %v", err)
		}
	}
	sess, err := r.cal.SessionAt(now)
	if err != nil || !sess.Trading {
		return
	}
	cfg := r.uc.Settings()
	switch {
	case now.Sub(r.lastFull) >= cfg.FullScanInterval:
		r.lastFull, r.lastScan = now, now
		r.takeAlerts(now, true)
		rep, err := r.uc.Scan(ctx, biz.ScanRequest{Pool: biz.PoolIntraday, Full: true, AsOf: now})
		r.report(rep, err)
		r.exits(ctx, now)
	case now.Sub(r.lastScan) >= cfg.ScanInterval:
		r.lastScan = now
		syms := r.takeAlerts(now, true)
		rep, err := r.uc.Scan(ctx, biz.ScanRequest{Pool: biz.PoolIntraday, Symbols: syms, AsOf: now})
		r.report(rep, err)
		r.exits(ctx, now)
	default:
		if syms := r.takeAlerts(now, false); len(syms) > 0 {
			rep, err := r.uc.Scan(ctx, biz.ScanRequest{Pool: biz.PoolIntraday, Symbols: syms, AsOf: now})
			r.report(rep, err)
		}
	}
}

func (r *Runner) exits(ctx context.Context, now time.Time) {
	ids, err := r.uc.ScanExits(ctx, now)
	if err != nil {
		r.log.Errorf("exit scan: %v", err)
		return
	}
	if len(ids) > 0 {
		r.log.Infof("sell signals %v", ids)
	}
}

// takeAlerts 取出待扫的异动代码。force 为假时要等最后一条异动过去 alertDebounce 才取。
func (r *Runner) takeAlerts(now time.Time, force bool) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.alerted) == 0 || (!force && now.Sub(r.alertAt) < alertDebounce) {
		return nil
	}
	out := make([]string, 0, len(r.alerted))
	for s := range r.alerted {
		out = append(out, s)
	}
	r.alerted = map[string]struct{}{}
	return out
}

func (r *Runner) report(rep *biz.ScanReport, err error) {
	if err != nil {
		r.log.Errorf("scan: %v", err)
		return
	}
	if rep == nil {
		return
	}
	r.log.Infof("scan pool=%s universe=%d matched=%d ranked=%d ai=%d degraded=%d signals=%v misses=%v elapsed=%s trace=%s",
		rep.Pool, rep.Universe, rep.Matched, rep.Ranked, rep.AICalls, rep.Degraded, rep.Signals, rep.Misses, rep.Elapsed, rep.TraceID)
}

type alertPayload struct {
	Symbol string `json:"symbol"`
	Kind   string `json:"kind"`
}

type killPayload struct {
	Active *bool  `json:"active"`
	Reason string `json:"reason"`
}

// subscribe 订阅 Kill Switch（从 JetStream 取最后一条恢复状态）和个股异动。
func (r *Runner) subscribe() error {
	if r.bus == nil {
		return nil
	}
	ks, err := r.bus.JetStream().Subscribe(events.SubjectKillSwitch, func(m *nats.Msg) {
		r.onKill(m.Data)
		_ = m.Ack()
	}, nats.DeliverLast(), nats.AckExplicit())
	if err != nil {
		return err
	}
	r.subs = append(r.subs, ks)
	al, err := r.bus.Conn().Subscribe(events.SubjectMarketAlert, func(m *nats.Msg) { r.onAlert(m.Data) })
	if err != nil {
		return err
	}
	r.subs = append(r.subs, al)
	return nil
}

// onKill 缺 active 字段时按触发处理。
func (r *Runner) onKill(body []byte) {
	env, err := events.Unmarshal(body)
	if err != nil {
		r.log.Warnf("killswitch: %v", err)
		return
	}
	var p killPayload
	_ = json.Unmarshal(env.Payload, &p)
	active := p.Active == nil || *p.Active
	r.uc.SetKillSwitch(active)
}

func (r *Runner) onAlert(body []byte) {
	env, err := events.Unmarshal(body)
	if err != nil || !r.seen.First(env.EventID) {
		return
	}
	var p alertPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.Symbol == "" {
		return
	}
	r.mu.Lock()
	r.alerted[p.Symbol] = struct{}{}
	r.alertAt = r.now()
	r.mu.Unlock()
}
