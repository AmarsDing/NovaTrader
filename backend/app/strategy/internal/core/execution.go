package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"server/pkg/events"
	"server/pkg/observe"
	"server/pkg/symbol"
)

func (r *Runner) executionLoop(ctx context.Context) {
	defer r.wg.Done()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	recover := func() {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		finish := observe.ForService("strategy").Op("execution_projection").Begin()
		next, changed, err := r.uc.ReconcileExecutionsPage(bounded, r.executionCursor, 200)
		observe.FinishError(finish, err)
		r.executionCursor = next
		if err != nil && ctx.Err() == nil {
			r.log.Warnf("execution projection: %v", err)
		}
		if changed > 0 {
			r.log.Infof("execution projections updated=%d", changed)
		}
	}
	recover()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			recover()
		}
	}
}

func (r *Runner) subscribeExecutions() error {
	var errs []error
	for _, item := range []struct{ subject, durable string }{
		{events.SubjectTradeOrder, "strategy-trade-order-v1"},
		{events.SubjectTradeFill, "strategy-trade-fill-v1"},
	} {
		sub, err := r.bus.JetStream().QueueSubscribe(item.subject, "strategy-execution-projection", func(msg *nats.Msg) {
			op := observe.ForService("strategy").Op("execution_projection")
			op.Received()
			finish := op.Begin()
			bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.onExecution(bounded, msg.Subject, msg.Data); err != nil {
				observe.FinishError(finish, err)
				r.log.Warnf("execution event projection: %v", err)
				_ = msg.NakWithDelay(2 * time.Second)
				return
			}
			finish(observe.Succeeded)
			_ = msg.Ack()
		}, nats.Durable(item.durable), nats.ManualAck(), nats.DeliverAll(), nats.AckWait(15*time.Second), nats.MaxAckPending(128))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		r.subs = append(r.subs, sub)
	}
	return errors.Join(errs...)
}

func executionOrderID(subject string, body []byte) (events.Envelope, string, error) {
	env, err := events.Unmarshal(body)
	if err != nil {
		return env, "", err
	}
	if env.Source != "trade" || env.Subject != subject || subject != events.SubjectTradeOrder && subject != events.SubjectTradeFill {
		return env, "", fmt.Errorf("strategy: invalid trade event identity")
	}
	var p struct {
		Snake  string `json:"client_order_id"`
		Legacy string `json:"ClientOrderID"`
	}
	if err = json.Unmarshal(env.Payload, &p); err != nil {
		return env, "", err
	}
	if p.Snake != "" && p.Legacy != "" && p.Snake != p.Legacy {
		return env, "", fmt.Errorf("strategy: conflicting order event identity")
	}
	id := p.Snake
	if id == "" {
		id = p.Legacy
	}
	if id == "" && subject == events.SubjectTradeOrder {
		// Historical ApplyBar published only this aggregate notification. It
		// contains no order/fill authority; the periodic ledger scan recovers it.
		var fields map[string]json.RawMessage
		var aggregate struct {
			Book   string `json:"book"`
			Symbol string `json:"symbol"`
			Count  int    `json:"count"`
		}
		if json.Unmarshal(env.Payload, &fields) == nil && len(fields) == 3 && fields["book"] != nil && fields["symbol"] != nil && fields["count"] != nil && json.Unmarshal(env.Payload, &aggregate) == nil && (aggregate.Book == "paper" || aggregate.Book == "live") && aggregate.Count > 0 && aggregate.Count <= 10000 {
			if _, err := symbol.Parse(aggregate.Symbol); err == nil {
				return env, "", nil
			}
		}
	}
	if id == "" || len(id) > 64 || strings.TrimSpace(id) != id {
		return env, "", fmt.Errorf("strategy: missing original order ID")
	}
	return env, id, nil
}

// Events are durable wakeups only. A payload's claimed fills or signal ID can
// never update a signal; the use case re-resolves the original order in SQL.
func (r *Runner) onExecution(ctx context.Context, subject string, body []byte) error {
	env, id, err := executionOrderID(subject, body)
	if err != nil {
		return err
	}
	if id == "" {
		return nil
	} // Explicit historical aggregate compatibility only.
	_, err = r.uc.ReconcileExecutionOrder(events.ContextWithEnvelope(ctx, env), id)
	return err
}
