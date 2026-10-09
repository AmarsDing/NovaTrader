// Package core 是 market 的常驻逻辑：按快照轮次刷新引擎，按交易日历跑预热、竞价、检查点、收盘、资金任务。
package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"server/app/market/internal/biz"
	"server/ent"
	"server/pkg/events"
	"server/pkg/scheduler"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/nats-io/nats.go"
)

var ProviderSet = wire.NewSet(NewWorker)

const (
	tickEvery = 20 * time.Second
	// 集合竞价 09:15 开始；15:05 收盘任务之后不再刷新。
	sessionStart = 9*time.Hour + 15*time.Minute
	sessionEnd   = 15*time.Hour + 5*time.Minute
)

// Worker 实现 kratos 的 transport.Server。
type Worker struct {
	uc     *biz.Usecase
	bus    *events.Bus
	cfg    biz.Config
	cal    *tradecal.Calendar
	sched  *scheduler.Scheduler
	log    *log.Helper
	kick   chan struct{}
	sub    *nats.Subscription
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewWorker(uc *biz.Usecase, client *ent.Client, bus *events.Bus, cfg biz.Config, logger log.Logger) (*Worker, error) {
	w := &Worker{
		uc: uc, bus: bus, cfg: cfg, cal: tradecal.Default,
		log:  log.NewHelper(log.With(logger, "module", "market/core")),
		kick: make(chan struct{}, 1),
	}
	jobs, err := w.jobs()
	if err != nil {
		return nil, err
	}
	w.sched = scheduler.New(w.cal, scheduler.EntStore{Client: client}, jobs...)
	return w, nil
}

// cronAt 把 HH:MM 转成五段 cron。
func cronAt(hhmm string) (string, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(hhmm))
	if err != nil {
		return "", fmt.Errorf("market: checkpoint %q: want HH:MM", hhmm)
	}
	return fmt.Sprintf("%d %d * * *", t.Minute(), t.Hour()), nil
}

func (w *Worker) jobs() ([]scheduler.Job, error) {
	now := func(fn func(context.Context, time.Time) error) func(context.Context) error {
		return func(ctx context.Context) error { return fn(ctx, time.Now()) }
	}
	jobs := []scheduler.Job{
		{Name: "market.warmup", Spec: "40 8 * * *", TradingDayOnly: true, Timeout: 5 * time.Minute, Retry: 2, Run: now(w.uc.Warmup)},
		{Name: "market.overseas", Spec: "50 8 * * *", TradingDayOnly: true, Timeout: time.Minute, Retry: 2, MaxDelay: 40 * time.Minute, Run: now(w.uc.Overseas)},
		// 09:25 撮合，09:30 前没有成交；晚于开盘就不再补竞价截面。
		{Name: "market.auction", Spec: "26 9 * * *", TradingDayOnly: true, Timeout: time.Minute, Retry: 1, MaxDelay: 3 * time.Minute, Run: now(w.uc.Auction)},
		// 重启晚于收盘一小时后，内存里已没有当天的分钟线，不再定稿。
		{Name: "market.close", Spec: "5 15 * * *", TradingDayOnly: true, Timeout: 3 * time.Minute, Retry: 2, MaxDelay: time.Hour, Run: now(w.uc.Close)},
		// M01 资金流 16:30、龙虎榜 17:30 入库。
		{Name: "market.capital", Spec: "30 19 * * *", TradingDayOnly: true, Timeout: 3 * time.Minute, Retry: 2, Run: now(w.uc.Capital)},
	}
	for _, at := range w.cfg.Checkpoints {
		spec, err := cronAt(at)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, scheduler.Job{
			Name: "market.checkpoint." + strings.ReplaceAll(strings.TrimSpace(at), ":", ""), Spec: spec,
			TradingDayOnly: true, Timeout: 2 * time.Minute, Retry: 1, MaxDelay: 5 * time.Minute,
			Run: now(w.uc.Checkpoint),
		})
	}
	return jobs, nil
}

func (w *Worker) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	sub, err := w.bus.Conn().Subscribe(events.SubjectMarketSnapshot, func(*nats.Msg) {
		select {
		case w.kick <- struct{}{}:
		default:
		}
	})
	if err != nil {
		cancel()
		return err
	}
	w.sub = sub
	w.wg.Add(2)
	go w.refreshLoop(runCtx)
	go w.scheduleLoop(runCtx)
	w.log.Infof("subscribed %s, refresh every %s", events.SubjectMarketSnapshot, w.cfg.RefreshInterval)
	return nil
}

func (w *Worker) Stop(ctx context.Context) error {
	if w.sub != nil {
		_ = w.sub.Unsubscribe()
	}
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

// inSession 判断 now 是否在交易日的刷新时段内。
func (w *Worker) inSession(now time.Time) bool {
	now = now.In(tradecal.Shanghai())
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if off := now.Sub(day); off < sessionStart || off > sessionEnd {
		return false
	}
	open, err := w.cal.Open(day)
	return err == nil && open
}

// refreshLoop 收到 M01 的轮次通知立即刷新；通知丢失时按 RefreshInterval 兜底。
func (w *Worker) refreshLoop(ctx context.Context) {
	defer w.wg.Done()
	t := time.NewTicker(w.cfg.RefreshInterval)
	defer t.Stop()
	last := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.kick:
		case <-t.C:
			if time.Since(last) < w.cfg.RefreshInterval {
				continue
			}
		}
		now := time.Now()
		if !w.inSession(now) {
			continue
		}
		last = now
		if err := w.uc.Refresh(ctx, now); err != nil && ctx.Err() == nil {
			w.log.Errorf("refresh: %v", err)
		}
	}
}

// scheduleLoop 启动时先预热（盘中重启可直接接上），之后定时检查计划任务。
func (w *Worker) scheduleLoop(ctx context.Context) {
	defer w.wg.Done()
	if err := w.uc.EnsureDefaults(ctx); err != nil && ctx.Err() == nil {
		w.log.Errorf("seed defaults: %v", err)
	}
	if err := w.uc.Warmup(ctx, time.Now()); err != nil && ctx.Err() == nil {
		w.log.Errorf("startup warmup: %v", err)
	}
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
