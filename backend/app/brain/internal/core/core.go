// Package core 是 brain 的常驻任务：交易日 08:30 晨报、15:35 收盘定稿、15:40 程序版复盘，以及 outbox 投递。
package core

import (
	"context"
	"sync"
	"time"

	"server/app/brain/internal/biz"
	"server/pkg/observe"
	"server/pkg/outbox"
	"server/pkg/scheduler"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewRunner)

const (
	jobBriefing      = "brain.morning_briefing"
	jobFrozen        = "brain.emotion_close_frozen"
	jobReviewProgram = "brain.review_program"
	jobVerify        = "brain.prediction_check"
	jobReviewAI      = "brain.review_ai"
	jobReviewWeekly  = "brain.review_ai_weekly"
	tickInterval     = 30 * time.Second
	dispatchInterval = time.Second
)

// Runner 实现 kratos transport.Server，随服务启停。
type Runner struct {
	uc    *biz.Usecase
	box   *outbox.Dispatcher
	sched *scheduler.Scheduler
	log   *log.Helper

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewRunner(uc *biz.Usecase, store scheduler.Store, box *outbox.Dispatcher, logger log.Logger) *Runner {
	r := &Runner{uc: uc, box: box, log: log.NewHelper(log.With(logger, "module", "brain/core"))}
	r.sched = scheduler.New(tradecal.Default, store,
		scheduler.Job{
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
		},
		// 15:35 收盘定稿：冻结当日情绪读数。数据源 15:32 前后落定，允许补，重试 6 次。
		scheduler.Job{
			Name:           jobFrozen,
			Spec:           "35 15 * * *",
			TradingDayOnly: true,
			Timeout:        5 * time.Minute,
			Retry:          6,
			MaxDelay:       90 * time.Minute,
			Run: func(ctx context.Context) error {
				_, err := uc.FreezeEmotion(ctx, time.Now())
				return err
			},
		},
		// 15:40 程序版每日复盘（八格），依赖定稿。
		scheduler.Job{
			Name:           jobReviewProgram,
			Spec:           "40 15 * * *",
			TradingDayOnly: true,
			DependsOn:      []string{jobFrozen},
			Timeout:        5 * time.Minute,
			Retry:          2,
			MaxDelay:       90 * time.Minute,
			Run: func(ctx context.Context) error {
				return uc.WriteProgramReview(ctx, time.Now())
			},
		},
		// 15:45 明日验证对账：上一交易日晨报候选 × 当日表现；并跑 source=signal 的
		// T+1/3/5 到期信号级对账（M13-T08）。两源均幂等 upsert，前源失败重跑会补后源。
		scheduler.Job{
			Name:           jobVerify,
			Spec:           "45 15 * * *",
			TradingDayOnly: true,
			DependsOn:      []string{jobFrozen},
			Timeout:        5 * time.Minute,
			Retry:          3,
			MaxDelay:       90 * time.Minute,
			Run: func(ctx context.Context) error {
				if _, err := uc.VerifyPredictions(ctx, time.Now()); err != nil {
					return err
				}
				_, err := uc.VerifySignals(ctx, time.Now())
				return err
			},
		},
		// 15:50 分段式 AI 复盘（P1-7）：情绪/资金/题材/龙虎榜/龙头/筹码供给独立会话 + 汇总，依赖定稿。
		scheduler.Job{
			Name:           jobReviewAI,
			Spec:           "50 15 * * *",
			TradingDayOnly: true,
			DependsOn:      []string{jobFrozen},
			Timeout:        5 * time.Minute,
			Retry:          2,
			MaxDelay:       90 * time.Minute,
			Run: func(ctx context.Context) error {
				return uc.GenerateAIReview(ctx, time.Now())
			},
		},
		// 15:55 AI 周报（P1-7b）：只在本 ISO 周最后一个交易日生成，落 kind=ai_week 并发 strategy.review 事件。
		scheduler.Job{
			Name:           jobReviewWeekly,
			Spec:           "55 15 * * *",
			TradingDayOnly: true,
			DependsOn:      []string{jobFrozen},
			Timeout:        5 * time.Minute,
			Retry:          2,
			MaxDelay:       90 * time.Minute,
			Run: func(ctx context.Context) error {
				now := time.Now()
				last, err := uc.IsWeekLastOpenDay(now)
				if err != nil {
					return err
				}
				if !last {
					return nil // 非本周最后交易日，跳过（下个交易日还在本 ISO 周内）
				}
				return uc.GenerateWeeklyReview(ctx, now)
			},
		},
	)
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
		finish := observe.ForService("brain").Op("outbox").Begin()
		_, err := r.box.Dispatch(ctx, 100)
		observe.FinishError(finish, err)
		if err != nil {
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
