package biz

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"server/pkg/events"
)

const chaseTick = 0.01

// SweepWorking 撤掉超时的盯盘限价单，并在追价上限内重报剩余数量。
func (e *Engine) SweepWorking(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock()
	var due []Order
	for _, status := range []string{StatusSubmitted, StatusPartial} {
		rows, err := e.store.ListByStatus(ctx, status)
		if err != nil {
			return err
		}
		for _, order := range rows {
			if chaseable(order, now, e.set.WorkTimeout) {
				due = append(due, order)
			}
		}
	}
	for _, order := range due {
		if err := e.chase(ctx, order); err != nil {
			return err
		}
	}
	return nil
}

func chaseable(order Order, now time.Time, timeout time.Duration) bool {
	if order.Source != "watch" {
		return false
	}
	if order.Status != StatusSubmitted && order.Status != StatusPartial {
		return false
	}
	if order.CreatedAt.IsZero() {
		return false
	}
	return !now.Before(order.CreatedAt.Add(timeout))
}

func (e *Engine) chase(ctx context.Context, order Order) error {
	fresh, ok, err := e.store.LoadOrder(ctx, order.ClientOrderID)
	if err != nil {
		return err
	}
	if !ok || !chaseable(fresh, e.clock(), e.set.WorkTimeout) {
		return nil
	}
	root, gen := splitChase(fresh.ClientOrderID)
	base := fresh.Price
	if gen > 0 {
		parent, ok, err := e.store.LoadOrder(ctx, root)
		if err != nil {
			return err
		}
		if ok && parent.Price > 0 {
			base = parent.Price
		}
	}
	remain := fresh.Volume - fresh.Filled
	next, okPrice := chasePrice(fresh.Side, fresh.Price, base, e.set.ChaseCap)
	reason := "超时撤单"
	if gen >= e.set.ChaseMax || remain <= 0 || !okPrice {
		if gen >= e.set.ChaseMax || !okPrice {
			reason = "超时撤单，已到追价上限"
		}
	}
	fresh.Status = StatusCancelled
	fresh.Reason = reason
	if err := e.store.Commit(ctx, Batch{
		Orders: []Order{fresh},
		Trails: []Trail{{ClientOrderID: fresh.ClientOrderID, Status: fresh.Status, Note: fresh.Reason}},
		Events: []Event{{Subject: events.SubjectTradeOrder, Payload: orderPayload(fresh)}},
	}); err != nil {
		return err
	}
	if reason != "超时撤单" || remain <= 0 {
		return nil
	}
	_, err = e.place(ctx, PlaceRequest{
		ClientOrderID:   root + "#" + strconv.Itoa(gen+1),
		Account:         accountOf(fresh.Book),
		Symbol:          fresh.Symbol,
		Side:            fresh.Side,
		Price:           next,
		Volume:          remain,
		Source:          "watch",
		Operator:        fresh.Operator,
		SignalID:        fresh.SignalID,
		StrategyVersion: fresh.StrategyVersion,
	})
	return err
}

func splitChase(id string) (string, int) {
	i := strings.LastIndex(id, "#")
	if i <= 0 || i == len(id)-1 {
		return id, 0
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil || n < 0 {
		return id, 0
	}
	return id[:i], n
}

func chasePrice(side string, current, base, cap float64) (float64, bool) {
	if side == "sell" {
		next := round2(current - chaseTick)
		floor := round2(base * (1 - cap))
		if next <= 0 || next < floor-1e-9 {
			return 0, false
		}
		return next, true
	}
	next := round2(current + chaseTick)
	ceil := round2(base * (1 + cap))
	if next > ceil+1e-9 {
		return 0, false
	}
	return next, true
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
