package core

import (
	"context"
	"encoding/json"
	"time"

	"server/app/trade/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/outbox"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/nats-io/nats.go"
)

var ProviderSet = wire.NewSet(NewRunner)

// Runner 每 10 秒记一次模拟账户快照，投递 outbox，并用分钟线撮合未完成的模拟委托。
type Runner struct {
	e        *biz.Engine
	bus      *events.Bus
	client   *ent.Client
	interval time.Duration
	log      *log.Helper
	sub      *nats.Subscription
	cancel   context.CancelFunc
}

func NewRunner(c *conf.Trade, e *biz.Engine, bus *events.Bus, client *ent.Client, logger log.Logger) *Runner {
	every := 10 * time.Second
	if c != nil && c.GetSnapshotInterval() != nil && c.GetSnapshotInterval().AsDuration() > 0 {
		every = c.GetSnapshotInterval().AsDuration()
	}
	return &Runner{e: e, bus: bus, client: client, interval: every, log: log.NewHelper(logger)}
}

func (r *Runner) Start(ctx context.Context) error {
	ctx, r.cancel = context.WithCancel(ctx)
	sub, err := r.bus.Conn().Subscribe(events.SubjectMarketBar, func(msg *nats.Msg) {
		var body struct {
			Bars []market.Bar `json:"bars"`
		}
		if err := json.Unmarshal(msg.Data, &body); err != nil {
			r.log.Warnf("bar: %v", err)
			return
		}
		for _, b := range body.Bars {
			if _, err := r.e.ApplyBar(context.Background(), "SIM", b.Symbol, biz.Quote{
				Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume,
			}); err != nil {
				r.log.Warnf("apply %s: %v", b.Symbol, err)
			}
		}
	})
	if err != nil {
		r.log.Warnf("subscribe bars: %v", err)
	} else {
		r.sub = sub
	}
	if _, err := r.bus.JetStream().Subscribe(events.SubjectRiskExit, r.onExit,
		nats.Durable("trade_risk_exit"), nats.ManualAck(), nats.DeliverAll()); err != nil {
		r.log.Warnf("subscribe exit: %v", err)
	}
	go r.loop(ctx)
	return nil
}

func (r *Runner) onExit(msg *nats.Msg) {
	env, err := events.Unmarshal(msg.Data)
	if err != nil {
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
	if err := json.Unmarshal(env.Payload, &body); err != nil || body.ExitID == "" || body.Volume <= 0 {
		r.log.Warnf("exit payload: %v", err)
		_ = msg.Ack()
		return
	}
	order, err := r.e.Place(context.Background(), biz.PlaceRequest{
		ClientOrderID: body.ExitID,
		Account:       body.Account,
		Symbol:        body.Symbol,
		Side:          "sell",
		Price:         body.Price,
		Volume:        body.Volume,
		Source:        "watch",
		Operator:      "risk",
		SignalID:      body.Trigger,
	})
	if err != nil {
		r.log.Warnf("exit place %s: %v", body.Symbol, err)
		_ = msg.Nak()
		return
	}
	r.log.Infof("exit order %s %s %s", order.ClientOrderID, order.Status, order.Reason)
	_ = msg.Ack()
}

func (r *Runner) Stop(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	if r.sub != nil {
		_ = r.sub.Unsubscribe()
	}
	return nil
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
			if _, err := r.e.Snapshot(context.Background(), "SIM"); err != nil {
				r.log.Warnf("snapshot: %v", err)
			}
		case <-dispatch.C:
			if err := r.e.ExpireConfirms(context.Background()); err != nil {
				r.log.Warnf("confirm: %v", err)
			}
			if err := r.e.SweepWorking(context.Background()); err != nil {
				r.log.Warnf("chase: %v", err)
			}
			if _, err := outbox.Dispatch(context.Background(), r.client, r.bus, 100); err != nil {
				r.log.Warnf("outbox: %v", err)
			}
		}
	}
}
