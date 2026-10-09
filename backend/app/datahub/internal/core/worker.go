package core

import (
	"context"
	"os"
	"sync"
	"time"

	"server/app/datahub/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/events"
	"server/pkg/registry"
	"server/pkg/scheduler"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewWorker)

const (
	tickEvery     = 20 * time.Second
	healthEvery   = time.Minute
	dispatchEvery = 500 * time.Millisecond
	backfillEvery = 2 * time.Second
	beatEvery     = 15 * time.Second
)

type Worker struct {
	col    *biz.Collector
	client *ent.Client
	bus    *events.Bus
	cfg    *conf.Datahub
	cal    *tradecal.Calendar
	sched  *scheduler.Scheduler
	log    *log.Helper
	addr   string
	id     string

	cancel context.CancelFunc
	wg     sync.WaitGroup
	bfMu   sync.Mutex
}

func NewWorker(col *biz.Collector, client *ent.Client, bus *events.Bus, cfg *conf.Datahub, logger log.Logger) (*Worker, error) {
	id, _ := os.Hostname()
	addr := ":2011"
	if h := cfg.GetHttp(); h != nil && h.GetAddr() != "" {
		addr = h.GetAddr()
	}
	w := &Worker{
		col: col, client: client, bus: bus, cfg: cfg, cal: tradecal.Default,
		log: log.NewHelper(log.With(logger, "module", "datahub/core")), addr: addr, id: id,
	}
	w.sched = scheduler.New(w.cal, scheduler.EntStore{Client: client}, w.jobs()...)
	return w, nil
}

func (w *Worker) jobs() []scheduler.Job {
	day := func(domain string) func(context.Context) error {
		return func(ctx context.Context) error { return w.collect(ctx, domain) }
	}
	return []scheduler.Job{
		{Name: "datahub.macro", Spec: "50 7 * * *", Timeout: 3 * time.Minute, Retry: 2, Run: day(biz.DomainMacro)},
		{Name: "datahub.security", Spec: "0 8 * * *", TradingDayOnly: true, Timeout: 3 * time.Minute, Retry: 2, Run: day(biz.DomainSecurity)},
		{Name: "datahub.sector", Spec: "10 8 * * *", TradingDayOnly: true, Timeout: 5 * time.Minute, Retry: 2, Run: day(biz.DomainSector)},
		{Name: "datahub.margin", Spec: "10 8 * * *", TradingDayOnly: true, Timeout: 3 * time.Minute, Retry: 2, Run: w.marginPrev},
		{Name: "datahub.overseas", Spec: "20 8 * * *", TradingDayOnly: true, Timeout: time.Minute, Retry: 2, Run: day(biz.DomainOverseas)},
		{Name: "datahub.finance", Spec: "20 8 * * *", TradingDayOnly: true, Timeout: 3 * time.Minute, Retry: 2, Run: day(biz.DomainFinance)},
		{Name: "datahub.daily_bar", Spec: "0 16 * * *", TradingDayOnly: true, Timeout: 20 * time.Minute, Retry: 2, Run: day(biz.DomainDailyBar)},
		{Name: "datahub.adj_factor", Spec: "20 16 * * *", TradingDayOnly: true, DependsOn: []string{"datahub.daily_bar"}, Timeout: 20 * time.Minute, Retry: 2, Run: day(biz.DomainAdjFactor)},
		{Name: "datahub.money_flow", Spec: "30 16 * * *", TradingDayOnly: true, Timeout: 5 * time.Minute, Retry: 2, Run: day(biz.DomainMoneyFlow)},
		{Name: "datahub.minute_bar", Spec: "40 16 * * *", TradingDayOnly: true, Timeout: 40 * time.Minute, Retry: 1, Run: day(biz.DomainMinuteBar)},
		{Name: "datahub.hsgt", Spec: "50 16 * * *", TradingDayOnly: true, Timeout: 3 * time.Minute, Retry: 2, Run: day(biz.DomainHsgtTop10)},
		{Name: "datahub.lhb", Spec: "30 17 * * *", TradingDayOnly: true, Timeout: 5 * time.Minute, Retry: 2, Run: day(biz.DomainLhb)},
		{Name: "datahub.reconcile", Spec: "0 18 * * *", TradingDayOnly: true, DependsOn: []string{"datahub.daily_bar"}, Timeout: 10 * time.Minute, Retry: 1, Run: w.reconcile},
		{Name: "datahub.purge_raw", Spec: "0 3 * * *", Timeout: time.Minute, Retry: 1, Run: w.purgeRaw},
	}
}

func (w *Worker) collect(ctx context.Context, domain string) error {
	if !w.col.Pipeline().DomainEnabled(domain) {
		return nil
	}
	_, err := w.col.RunDomain(ctx, domain, "", "")
	if err != nil {
		w.log.Errorf("%s: %v", domain, err)
	}
	return err
}

func (w *Worker) marginPrev(ctx context.Context) error {
	if !w.col.Pipeline().DomainEnabled(biz.DomainMargin) {
		return nil
	}
	day, err := w.col.PreviousOpenDay(time.Now())
	if err != nil {
		return err
	}
	_, err = w.col.CollectMargin(ctx, day, "")
	return err
}

func (w *Worker) reconcile(ctx context.Context) error {
	day, err := w.col.LastOpenDay(time.Now())
	if err != nil {
		return err
	}
	_, err = w.col.Reconcile(ctx, day, biz.ReconcileOptions{})
	return err
}

func (w *Worker) purgeRaw(ctx context.Context) error {
	keep := time.Duration(w.cfg.GetRawKeepDays()) * 24 * time.Hour
	n, err := w.col.PurgeRaw(ctx, keep)
	if n > 0 {
		w.log.Infof("purged %d raw responses", n)
	}
	return err
}

func (w *Worker) Start(ctx context.Context) error {
	if w.cfg.GetJobsDisabled() {
		w.log.Info("jobs disabled")
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Add(6)
	go w.scheduleLoop(runCtx)
	go w.snapshotLoop(runCtx)
	go w.intradayLoop(runCtx)
	go w.intelLoop(runCtx)
	go w.maintainLoop(runCtx)
	go w.backfillLoop(runCtx)
	w.recoverRunning(runCtx)
	w.log.Info("collector loops started")
	return nil
}

func (w *Worker) recoverRunning(ctx context.Context) {
	jobs, err := w.col.RecoverBackfills(ctx)
	if err != nil {
		w.log.Errorf("recover backfill: %v", err)
		return
	}
	for _, job := range jobs {
		if job.Status != biz.JobRunning {
			continue
		}
		w.log.Infof("resume interrupted backfill %d %s", job.ID, job.Domain)
		go func(job biz.BackfillJob) {
			w.bfMu.Lock()
			defer w.bfMu.Unlock()
			w.col.RunBackfill(ctx, job)
		}(job)
	}
}

func (w *Worker) Stop(ctx context.Context) error {
	if w.cancel != nil {
		w.cancel()
	}
	done := make(chan struct{})
	go func() { w.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return nil
}

func (w *Worker) scheduleLoop(ctx context.Context) {
	defer w.wg.Done()
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		if err := w.sched.Tick(ctx, time.Now()); err != nil && ctx.Err() == nil {
			w.log.Errorf("scheduler: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Worker) snapshotLoop(ctx context.Context) {
	defer w.wg.Done()
	every := 3 * time.Second
	if s := w.cfg.GetSnapshot(); s != nil && s.GetInterval() != nil && s.GetInterval().AsDuration() > 0 {
		every = s.GetInterval().AsDuration()
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !w.col.Pipeline().DomainEnabled(biz.DomainSnapshot) {
				continue
			}
			sess, err := w.cal.SessionAt(time.Now())
			if err != nil || !biz.InQuoteSession(sess) {
				continue
			}
			if _, err := w.col.CollectSnapshot(ctx); err != nil && ctx.Err() == nil {
				w.log.Errorf("snapshot: %v", err)
			}
		}
	}
}

func (w *Worker) intradayLoop(ctx context.Context) {
	defer w.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	last := map[string]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sess, err := w.cal.SessionAt(time.Now())
			if err != nil || !sess.Trading {
				continue
			}
			w.maybe(ctx, biz.DomainLimitPool, 10*time.Second, last, func(ctx context.Context) error {
				_, err := w.col.CollectLimitPool(ctx, time.Now(), "")
				return err
			})
			w.maybe(ctx, biz.DomainSectorQuote, 10*time.Second, last, func(ctx context.Context) error {
				_, err := w.col.CollectSectorQuotes(ctx)
				return err
			})
			w.maybe(ctx, biz.DomainMoneyFlow, 10*time.Second, last, func(ctx context.Context) error {
				_, err := w.col.CollectIntradayFlow(ctx)
				return err
			})
		}
	}
}

func (w *Worker) intelLoop(ctx context.Context) {
	defer w.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	last := map[string]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.maybe(ctx, biz.DomainFlash, time.Minute, last, func(ctx context.Context) error {
				_, err := w.col.CollectIntel(ctx, biz.DomainFlash)
				return err
			})
			w.maybe(ctx, biz.DomainNews, time.Minute, last, func(ctx context.Context) error {
				_, err := w.col.CollectIntel(ctx, biz.DomainNews)
				return err
			})
			w.maybe(ctx, biz.DomainAnnouncement, time.Minute, last, func(ctx context.Context) error {
				_, err := w.col.CollectIntel(ctx, biz.DomainAnnouncement)
				return err
			})
			w.maybe(ctx, biz.DomainReport, 10*time.Minute, last, func(ctx context.Context) error {
				_, err := w.col.CollectIntel(ctx, biz.DomainReport)
				return err
			})
			w.maybe(ctx, biz.DomainHotRank, 5*time.Minute, last, func(ctx context.Context) error {
				_, err := w.col.CollectHotRank(ctx)
				return err
			})
		}
	}
}

func (w *Worker) maybe(ctx context.Context, domain string, def time.Duration, last map[string]time.Time, fn func(context.Context) error) {
	if !w.col.Pipeline().DomainEnabled(domain) {
		return
	}
	every := def
	if dc, ok := w.col.Pipeline().DomainConfig(domain); ok && dc.Interval > 0 {
		every = dc.Interval
	}
	if time.Since(last[domain]) < every {
		return
	}
	last[domain] = time.Now()
	if err := fn(ctx); err != nil && ctx.Err() == nil {
		w.log.Errorf("%s: %v", domain, err)
	}
}

func (w *Worker) maintainLoop(ctx context.Context) {
	defer w.wg.Done()
	health := time.NewTicker(healthEvery)
	dispatch := time.NewTicker(dispatchEvery)
	beat := time.NewTicker(beatEvery)
	defer health.Stop()
	defer dispatch.Stop()
	defer beat.Stop()
	w.beat(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-health.C:
			if err := w.col.PersistHealth(ctx); err != nil && ctx.Err() == nil {
				w.log.Errorf("health: %v", err)
			}
		case <-dispatch.C:
			if _, err := w.col.DispatchOutbox(ctx); err != nil && ctx.Err() == nil {
				w.log.Errorf("outbox: %v", err)
			}
		case <-beat.C:
			w.beat(ctx)
		}
	}
}

func (w *Worker) beat(ctx context.Context) {
	if err := registry.Beat(ctx, w.client, "datahub", w.id, w.addr); err != nil && ctx.Err() == nil {
		w.log.Errorf("heartbeat: %v", err)
	}
}

func (w *Worker) backfillLoop(ctx context.Context) {
	defer w.wg.Done()
	t := time.NewTicker(backfillEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			jobs, err := w.col.RecoverBackfills(ctx)
			if err != nil {
				w.log.Errorf("backfill list: %v", err)
				continue
			}
			for _, job := range jobs {
				if ctx.Err() != nil {
					return
				}
				if job.Status != biz.JobPending {
					continue
				}
				w.bfMu.Lock()
				done := w.col.RunBackfill(ctx, job)
				w.bfMu.Unlock()
				w.log.Infof("backfill %d %s %s rows=%d", done.ID, done.Domain, done.Status, done.Rows)
			}
		}
	}
}
