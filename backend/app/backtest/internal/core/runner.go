// Package core 是 backtest 的常驻部分：认领任务的工作协程，以及 15:30 自省、16:00 影子补记。
package core

import (
	"context"
	"os"
	"sync"
	"time"

	"server/app/backtest/internal/biz"
	"server/conf"
	"server/pkg/registry"
	"server/pkg/scheduler"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewRunner)

const (
	idleWait   = time.Second
	errorWait  = 3 * time.Second
	schedEvery = 30 * time.Second
	beatEvery  = 15 * time.Second
	jobIntro   = "m07-introspect"
	jobShadow  = "m07-shadow"
)

// Runner 实现 kratos transport.Server。
type Runner struct {
	uc      *biz.Usecase
	beater  *registry.Beater
	sched   *scheduler.Scheduler
	workers int
	log     *log.Helper
	addr    string
	id      string

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewRunner(uc *biz.Usecase, store scheduler.Store, beat *registry.Beater, cfg *conf.Backtest, logger log.Logger) *Runner {
	id, _ := os.Hostname()
	addr := ":2071" // 端口默认值，configs/config.yaml backtest.http.addr 可覆盖
	if h := cfg.GetHttp(); h != nil && h.GetAddr() != "" {
		addr = h.GetAddr()
	}
	r := &Runner{
		uc: uc, beater: beat, workers: uc.Settings().Workers,
		log: log.NewHelper(log.With(logger, "module", "backtest/core")), addr: addr, id: id,
	}
	if r.workers < 1 {
		r.workers = 1
	}
	r.sched = scheduler.New(tradecal.Default, store,
		scheduler.Job{
			Name: jobIntro, Spec: "30 15 * * *", TradingDayOnly: true,
			Timeout: 10 * time.Minute, Retry: 1, MaxDelay: 6 * time.Hour,
			Run: func(ctx context.Context) error {
				day, err := uc.LastClosed()
				if err != nil {
					return err
				}
				in, err := uc.Introspect(ctx, day, "")
				if err != nil {
					return err
				}
				r.log.Infof("introspect %s samples=%d win=%.2f triggered=%v task=%v",
					in.TradeDate.Format("2006-01-02"), in.SampleCount, in.WinRate, in.Triggered, in.TaskID)
				return nil
			},
		},
		scheduler.Job{
			Name: jobShadow, Spec: "0 16 * * *", TradingDayOnly: true,
			Timeout: 10 * time.Minute, Retry: 1, MaxDelay: 8 * time.Hour,
			Run: func(ctx context.Context) error {
				added, updated, err := uc.SyncShadow(ctx)
				r.log.Infof("shadow added=%d updated=%d err=%v", added, updated, err)
				return err
			},
		},
	)
	return r
}

func (r *Runner) Start(ctx context.Context) error {
	if err := r.uc.Recover(ctx); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	for i := 0; i < r.workers; i++ {
		r.wg.Add(1)
		go r.work(runCtx, i)
	}
	r.wg.Add(1)
	go r.schedule(runCtx)
	r.wg.Add(1)
	go r.beatLoop(runCtx)
	r.log.Infof("backtest core started, workers=%d", r.workers)
	return nil
}

func (r *Runner) Stop(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *Runner) work(ctx context.Context, n int) {
	defer r.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		ran, err := r.uc.RunNext(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			r.log.Errorf("worker %d: %v", n, err)
			if !sleep(ctx, errorWait) {
				return
			}
		case !ran:
			if !sleep(ctx, idleWait) {
				return
			}
		}
	}
}

func (r *Runner) schedule(ctx context.Context) {
	defer r.wg.Done()
	t := time.NewTicker(schedEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := r.sched.Tick(ctx, now); err != nil && ctx.Err() == nil {
				r.log.Errorf("scheduler: %v", err)
			}
		}
	}
}

func (r *Runner) beat(ctx context.Context) {
	if err := r.beater.Beat(ctx, "backtest", r.id, r.addr); err != nil && ctx.Err() == nil {
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

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
