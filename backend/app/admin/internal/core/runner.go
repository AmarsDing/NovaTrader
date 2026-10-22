// Package core 是 admin 的常驻逻辑：sys.session.changed 广播与每日备份调度。
package core

import (
	"context"
	"errors"
	"sync"
	"time"

	"server/app/admin/internal/data"
	"server/pkg/events"
	"server/pkg/scheduler"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewRunner)

const (
	schedEvery   = 30 * time.Second
	sessionEvery = 15 * time.Second
	jobBackup    = "admin.daily_backup"
)

// Runner 常驻两项职责：
// 1) 时段监察器——tradecal 边界一变就发 sys.session.changed（M00 §1：每次变化发）；
// 2) 每日备份调度——scheduler + task_run 记录，错过的时点按 MaxDelay 补跑。
// NATS 未连接时只降级广播，备份调度照常；库未配置时备份任务报错重试，不静默。
type Runner struct {
	watcher *tradecal.Watcher
	sched   *scheduler.Scheduler
	log     *log.Helper
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewRunner(audit *data.AuditStore, store scheduler.Store, bus *events.Bus, logger log.Logger) *Runner {
	helper := log.NewHelper(log.With(logger, "module", "admin/core"))
	r := &Runner{log: helper}
	r.watcher = tradecal.NewWatcher(tradecal.Default, sessionEvery, func(ctx context.Context, s tradecal.Session) error {
		if bus == nil {
			return errors.New("events: 无连接")
		}
		env, err := events.NewWithContext(ctx, "admin", events.SubjectSessionChanged, map[string]any{
			"phase":       string(s.Phase),
			"late":        s.Late,
			"trading":     s.Trading,
			"boundary_at": time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			return err
		}
		return bus.Publish(ctx, env)
	}, helper.Warnf)
	r.sched = scheduler.New(tradecal.Default, store, scheduler.Job{
		// 每日 03:30（上海）逻辑备份，不限交易日；错过时点 12h 内补跑。
		Name: jobBackup, Spec: "30 3 * * *",
		Timeout: 12 * time.Minute, Retry: 1, MaxDelay: 12 * time.Hour,
		Run: func(ctx context.Context) error {
			res, err := audit.Backup(ctx)
			if err != nil {
				return err
			}
			r.log.Infof("daily backup ok: %s %d bytes %dms", res.Path, res.Size, res.DurationMs)
			return nil
		},
	})
	return r
}

func (r *Runner) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Add(2)
	go func() {
		defer r.wg.Done()
		if err := r.watcher.Run(ctx); err != nil && ctx.Err() == nil {
			r.log.Errorf("session watcher: %v", err)
		}
	}()
	go r.schedule(ctx)
	r.log.Info("admin core started: session publisher + daily backup")
	return nil
}

func (r *Runner) Stop(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
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
