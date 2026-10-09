// Package core 是 brain 的常驻任务：交易日 08:30 晨报，以及 outbox 投递。
package core

import (
	"context"
	"sync"
	"time"

	"server/app/brain/internal/biz"
	"server/ent"
	"server/pkg/events"
	"server/pkg/outbox"
	"server/pkg/scheduler"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewRunner)

const (
	jobBriefing      = "brain.morning_briefing"
	tickInterval     = 30 * time.Second
	dispatchInterval = time.Second
)

// Runner 实现 kratos transport.Server，随服务启停。
type Runner struct {
	uc    *biz.Usecase
	db    *ent.Client
	bus   *events.Bus
	sched *scheduler.Scheduler
	log   *log.Helper

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewRunner(uc *biz.Usecase, db *ent.Client, bus *events.Bus, logger log.Logger) *Runner {
	r := &Runner{uc: uc, db: db, bus: bus, log: log.NewHelper(log.With(logger, "module", "brain/core"))}
	r.sched = scheduler.New(tradecal.Default, scheduler.EntStore{Client: db}, scheduler.Job{
		Name:           jobBriefing,
		Spec:           uc.Settings().BriefingCron,
		TradingDayOnly: true,
		Timeout:        5 * time.Minute,
		Retry:          1,
		MaxDelay:       time.Hour,
		Run: func(ctx context.Context) error {
			_, err := uc.GenerateBriefing(ctx, time.Now())
			return err
		},
	})
	return r
}

func (r *Runner) Start(ctx context.Context) error {
	ctx, r.cancel = context.WithCancel(context.WithoutCancel(ctx))
	r.wg.Add(2)
	go r.loop(ctx, tickInterval, func(ctx context.Context) {
		if err := r.sched.Tick(ctx, time.Now()); err != nil {
			r.log.Errorf("scheduler tick: %v", err)
		}
	})
	go r.loop(ctx, dispatchInterval, func(ctx context.Context) {
		if _, err := outbox.Dispatch(ctx, r.db, r.bus, 100); err != nil {
			r.log.Warnf("outbox dispatch: %v", err)
		}
	})
	return nil
}

func (r *Runner) Stop(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *Runner) loop(ctx context.Context, every time.Duration, fn func(context.Context)) {
	defer r.wg.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	fn(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}
