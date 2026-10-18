package biz

import (
	"context"
	"fmt"
	"math"
	stockSymbol "server/pkg/symbol"
	"time"
)

func (e *Engine) ApplyBar(ctx context.Context, account, symbol string, q Quote) (out []Order, err error) {
	result, err := e.ApplySourceBar(ctx, account, symbol, q)
	return result.Orders, err
}

type BarApplication struct {
	Orders   []Order
	Consumed bool
}

func (e *Engine) ApplySourceBar(ctx context.Context, account, symbol string, q Quote) (result BarApplication, err error) {
	var out []Order
	e.mu.Lock()
	defer e.mu.Unlock()
	err = e.withLedger(ctx, "global", func(c context.Context) error {
		book, err := bookOf(account)
		if err != nil || book != BookPaper {
			return fmt.Errorf("%w: only SIM may match source bars", ErrInvalid)
		}
		parsed, err := stockSymbol.Parse(symbol)
		if err != nil || parsed.Tongdaxin() != symbol {
			return fmt.Errorf("%w: canonical SIM bar symbol required", ErrInvalid)
		}
		// Zero-time quotes belong to existing isolated/manual simulation paths.
		// The runtime consumer requires source time before it calls this method.
		if q.Time.IsZero() {
			out, err = e.applyBar(c, account, symbol, q)
			result.Consumed = err == nil
			return err
		}
		if q.Close <= 0 || math.IsNaN(q.Close) || math.IsInf(q.Close, 0) || !q.Time.Equal(q.Time.UTC().Truncate(time.Minute)) {
			return fmt.Errorf("%w: validated SIM minute close/time required", ErrInvalid)
		}
		cursor, ok := e.store.(SimBarStore)
		if !ok {
			return fmt.Errorf("%w: durable SIM bar cursor unavailable", ErrInvalid)
		}
		start := q.Time.UTC().Truncate(time.Minute)
		last, err := cursor.LastConsumedSimBar(c, book, symbol)
		if err != nil {
			return err
		}
		if !start.After(last) {
			return nil
		}
		out, err = e.applyBar(c, account, symbol, q)
		if err != nil {
			return err
		}
		err = cursor.AdvanceSimBar(c, book, symbol, start, q.Close)
		result.Consumed = err == nil
		return err
	})
	result.Orders = out
	if err != nil {
		result.Consumed = false
	}
	return
}
func (e *Engine) Cancel(ctx context.Context, id string) (out Order, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	err = e.withLedger(ctx, "global", func(c context.Context) error { out, err = e.cancelOrder(c, id); return err })
	return
}
func (e *Engine) Confirm(ctx context.Context, id string) (out Order, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	err = e.withLedger(ctx, "global", func(c context.Context) error { out, err = e.confirm(c, id); return err })
	return
}
func (e *Engine) Snapshot(ctx context.Context, account string) (out AccountView, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	err = e.withLedger(ctx, "global", func(c context.Context) error { out, err = e.snapshotAccount(c, account); return err })
	return
}
func (e *Engine) RollDay(ctx context.Context, account string) (out AccountView, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	err = e.withLedger(ctx, "global", func(c context.Context) error { out, err = e.rollDay(c, account); return err })
	return
}
func (e *Engine) Reconcile(ctx context.Context, account string, remote []RemotePosition) (matched bool, diffs []string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	err = e.withLedger(ctx, "global", func(c context.Context) error { matched, diffs, err = e.reconcile(c, account, remote); return err })
	return
}
func (e *Engine) ExpireConfirms(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.withLedger(ctx, "global", e.expireConfirms)
}
func (e *Engine) SweepWorking(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.withLedger(ctx, "global", e.sweepWorking)
}
