// Package kbjobs 是 M04 知识库的常驻任务：信号转案例、补嵌入、月度清理。
package kbjobs

import (
	"context"
	"errors"
	"sync"
	"time"

	v1 "server/api/intel/v1"
	"server/app/intel/internal/biz/kb"
	"server/app/intel/internal/service/kbsvc"
	"server/conf"
	"server/pkg/errcode"
	"server/pkg/events"
	"server/pkg/scheduler"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	consumerName = "intel-kb-case"
	embedEvery   = 5 * time.Minute
	tickEvery    = time.Minute
	purgeJob     = "kb.purge"
	purgeSpec    = "30 3 1 * *"
	// purgeWindow 比调度器的 48 小时回看略短，窗口外不调用 Tick。
	purgeWindow = 47 * time.Hour
)

// Runner 实现 kratos transport.Server，随服务启停。
type Runner struct {
	uc      *kb.KnowledgeUsecase
	sched   *scheduler.Scheduler
	bus     *events.Bus
	consume bool
	log     *log.Helper

	cancel context.CancelFunc
	wg     sync.WaitGroup
	sub    *nats.Subscription
}

func New(uc *kb.KnowledgeUsecase, store scheduler.Store, bus *events.Bus, c *conf.Kb, logger log.Logger) *Runner {
	r := &Runner{
		uc:      uc,
		bus:     bus,
		consume: c.GetConsumeSignals(),
		log:     log.NewHelper(log.With(logger, "module", "kb.jobs")),
	}
	r.sched = scheduler.New(tradecal.Default, store, scheduler.Job{
		Name:     purgeJob,
		Spec:     purgeSpec,
		Timeout:  10 * time.Minute,
		MaxDelay: purgeWindow,
		Run: func(ctx context.Context) error {
			n, err := uc.Purge(ctx)
			if err == nil {
				r.log.Infof("purged %d documents", n)
			}
			return err
		},
	})
	return r
}

func (r *Runner) Start(ctx context.Context) error {
	ctx, r.cancel = context.WithCancel(context.Background())
	if r.consume && r.bus != nil {
		sub, err := r.bus.JetStream().QueueSubscribe(events.SubjectSignal, consumerName,
			func(m *nats.Msg) { r.handleSignal(ctx, m) },
			nats.Durable(consumerName),
			nats.ManualAck(),
			nats.AckWait(30*time.Second),
			nats.MaxDeliver(20),
		)
		if err != nil {
			r.cancel()
			return err
		}
		r.sub = sub
	}
	r.loop(ctx, embedEvery, r.embedPending)
	r.loop(ctx, tickEvery, r.tickPurge)
	return nil
}

// Stop 不退订：JetStream 退订会删掉持久消费者，连接关闭时订阅自然结束。
func (r *Runner) Stop(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *Runner) loop(ctx context.Context, every time.Duration, fn func(context.Context)) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			fn(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (r *Runner) embedPending(ctx context.Context) {
	n, err := r.uc.EmbedPending(ctx)
	if err != nil && ctx.Err() == nil {
		r.log.Warnf("embed pending: %v", err)
	}
	if n > 0 {
		r.log.Infof("embedded %d pending chunks", n)
	}
}

func (r *Runner) tickPurge(ctx context.Context) {
	now := time.Now().In(tradecal.Shanghai())
	if !InPurgeWindow(now) {
		return
	}
	if err := r.sched.Tick(ctx, now); err != nil && ctx.Err() == nil {
		r.log.Errorf("purge: %v", err)
	}
}

// InPurgeWindow 判断 now 是否在本月清理时刻之后的补跑窗口内。
func InPurgeWindow(now time.Time) bool {
	now = now.In(tradecal.Shanghai())
	slot := time.Date(now.Year(), now.Month(), 1, 3, 30, 0, 0, tradecal.Shanghai())
	return !now.Before(slot) && now.Sub(slot) < purgeWindow
}

var payloadJSON = protojson.UnmarshalOptions{DiscardUnknown: true}

// handleSignal 只处理终态。载荷坏了终止投递；业务校验失败终止并记日志；其余错误稍后重投。
func (r *Runner) handleSignal(ctx context.Context, m *nats.Msg) {
	if ctx.Err() != nil {
		_ = m.Nak()
		return
	}
	env, err := events.Unmarshal(m.Data)
	if err != nil {
		r.log.Errorf("bad signal envelope: %v", err)
		_ = m.Term()
		return
	}
	var p v1.KbSignalCase
	if err := payloadJSON.Unmarshal(env.Payload, &p); err != nil {
		r.log.Errorf("bad signal payload %s: %v", env.EventID, err)
		_ = m.Term()
		return
	}
	if !kb.IsTerminal(p.GetStatus()) {
		_ = m.Ack()
		return
	}
	sc, err := kbsvc.SignalCaseFromProto(&p)
	if err != nil {
		r.log.Errorf("bad signal payload %s: %v", env.EventID, err)
		_ = m.Term()
		return
	}
	if sc.ClosedAt.IsZero() {
		sc.ClosedAt = env.Time
	}
	res, err := r.uc.RecordCase(events.WithTraceID(ctx, env.TraceID), sc)
	var ec *errcode.Error
	switch {
	case errors.As(err, &ec) && ec.Code == errcode.Invalid:
		r.log.Errorf("signal %s rejected: %v", env.EventID, err)
		_ = m.Term()
	case err != nil:
		r.log.Warnf("signal %s retry: %v", env.EventID, err)
		_ = m.NakWithDelay(30 * time.Second)
	default:
		if res.Created {
			r.log.Infof("case %d from %s signal %d", res.CaseID, sc.Book, sc.SignalID)
		}
		_ = m.Ack()
	}
}
