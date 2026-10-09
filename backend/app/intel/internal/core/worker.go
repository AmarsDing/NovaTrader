// Package core 是 intel 的常驻逻辑：订阅 intel.raw.>、投递 outbox、定时刷新词典。
package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"server/app/intel/internal/biz"
	"server/ent"
	"server/pkg/events"
	"server/pkg/outbox"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/nats-io/nats.go"
)

var ProviderSet = wire.NewSet(NewWorker)

const (
	consumerName    = "intel"
	workers         = 4
	ackWait         = 2 * time.Minute
	maxDeliver      = 5
	retryDelay      = 5 * time.Second
	dispatchEvery   = 500 * time.Millisecond
	dictionaryEvery = 10 * time.Minute
)

// Worker 实现 kratos 的 transport.Server，随应用启动和停止。
type Worker struct {
	uc     *biz.Usecase
	client *ent.Client
	bus    *events.Bus
	log    *log.Helper

	msgs   chan *nats.Msg
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewWorker(uc *biz.Usecase, client *ent.Client, bus *events.Bus, logger log.Logger) *Worker {
	return &Worker{uc: uc, client: client, bus: bus, log: log.NewHelper(log.With(logger, "module", "intel/core"))}
}

func (w *Worker) Start(ctx context.Context) error {
	if err := w.uc.Warm(ctx); err != nil {
		return err
	}
	if n, err := w.uc.Recover(ctx); err != nil {
		w.log.Errorf("recover pending items: %v", err)
	} else if n > 0 {
		w.log.Infof("recovered %d pending items", n)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.msgs = make(chan *nats.Msg, workers*4)
	for i := 0; i < workers; i++ {
		w.wg.Add(1)
		go w.consume(runCtx)
	}
	_, err := w.bus.JetStream().QueueSubscribe(events.SubjectIntelRawAll, consumerName,
		func(m *nats.Msg) {
			select {
			case w.msgs <- m:
			case <-runCtx.Done():
			}
		},
		nats.Durable(consumerName),
		nats.ManualAck(),
		nats.AckWait(ackWait),
		nats.MaxDeliver(maxDeliver),
		nats.MaxAckPending(workers*8),
	)
	if err != nil {
		cancel()
		return err
	}
	w.wg.Add(2)
	go w.loop(runCtx, dispatchEvery, w.dispatch)
	go w.loop(runCtx, dictionaryEvery, w.reloadDictionary)
	w.log.Infof("subscribed %s as %s", events.SubjectIntelRawAll, consumerName)
	return nil
}

// Stop 不调用 Unsubscribe / Drain：持久消费者由 nats.go 创建时，这两个方法会把消费者删掉，
// 下次启动就会从头重放整个流。连接由 data.NewBus 的清理函数关闭，消费者和进度留在服务端。
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

func (w *Worker) consume(ctx context.Context) {
	defer w.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-w.msgs:
			w.handle(ctx, m)
		}
	}
}

// handle 处理一条 intel.raw.<kind>。坏消息直接终止投递；其他错误稍后重投，靠库上的去重保证幂等。
func (w *Worker) handle(ctx context.Context, m *nats.Msg) {
	env, err := events.Unmarshal(m.Data)
	if err != nil {
		w.log.Warnf("drop bad envelope on %s: %v", m.Subject, err)
		_ = m.Term()
		return
	}
	var raw biz.Raw
	if err := json.Unmarshal(env.Payload, &raw); err != nil {
		w.log.Warnf("drop bad payload event=%s: %v", env.EventID, err)
		_ = m.Term()
		return
	}
	if raw.Kind == "" {
		raw.Kind = strings.TrimPrefix(m.Subject, events.SubjectIntelRawPrefix)
	}
	res, err := w.uc.Process(events.WithTraceID(ctx, env.TraceID), raw)
	switch {
	case errors.Is(err, biz.ErrInvalid):
		w.log.Warnf("drop invalid item event=%s: %v", env.EventID, err)
		_ = m.Term()
	case err != nil:
		if meta, merr := m.Metadata(); merr == nil && meta.NumDelivered >= maxDeliver {
			w.log.Errorf("give up event=%s after %d deliveries: %v", env.EventID, meta.NumDelivered, err)
		} else {
			w.log.Errorf("process event=%s: %v", env.EventID, err)
		}
		_ = m.NakWithDelay(retryDelay)
	default:
		_ = m.Ack()
		w.log.Debugf("event=%s news=%d cluster=%d status=%s", env.EventID, res.NewsID, res.ClusterID, res.Status)
	}
}

func (w *Worker) loop(ctx context.Context, every time.Duration, fn func(context.Context)) {
	defer w.wg.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

func (w *Worker) dispatch(ctx context.Context) {
	if _, err := outbox.Dispatch(ctx, w.client, w.bus, 100); err != nil && ctx.Err() == nil {
		w.log.Warnf("outbox dispatch: %v", err)
	}
}

func (w *Worker) reloadDictionary(ctx context.Context) {
	if _, err := w.uc.ReloadDictionary(ctx); err != nil && ctx.Err() == nil {
		w.log.Warnf("reload dictionary: %v", err)
	}
}
