package biz

import (
	"context"
	"errors"
	"fmt"
	"math"
	"server/pkg/events"
)

// ErrExecutionProtected means expiry/cancellation cannot erase an authoritative
// order or an unresolved original intent while its projection is catching up.
var ErrExecutionProtected = errors.New("strategy: execution evidence protects signal")

type ExecutionOrder struct {
	ClientOrderID, Book, Symbol, Side, Status string
	Volume, Filled                            int
}
type ExecutionFill struct {
	ID                                int
	ClientOrderID, Book, Symbol, Side string
	Qty                               int
	Price, Amount                     float64
}
type ExecutionFacts struct {
	Orders           []ExecutionOrder
	Fills            []ExecutionFill
	UnresolvedIntent bool
}
type ExecutionProjection struct {
	Status         Status
	Reason         string
	ExecutedVolume *int
	ExecutedPrice  *float64
}
type ExecutionPlanner func(*Signal, ExecutionFacts) (ExecutionProjection, error)

// ExecutionProjectionRepo locks the signal, reads the authoritative trade ledger
// and writes its resulting signal/event/outbox projection in one transaction.
// It never trusts prices, fills or statuses supplied in an event payload.
type ExecutionProjectionRepo interface {
	ResolveExecutionSignal(context.Context, string) (int, error)
	ProjectExecution(context.Context, int, ExecutionPlanner, EnvelopeFunc) (*Signal, bool, error)
	ExecutionCandidates(context.Context, int, int) ([]int, error)
}

func planExecution(cur *Signal, facts ExecutionFacts) (ExecutionProjection, error) {
	out := ExecutionProjection{Status: cur.Status}
	if len(facts.Orders) == 0 {
		if len(facts.Fills) != 0 {
			return out, fmt.Errorf("%w: fills without original orders", ErrBadRequest)
		}
		return out, nil
	}
	orders := map[string]ExecutionOrder{}
	active := false
	for _, order := range facts.Orders {
		if order.ClientOrderID == "" || order.Book != cur.Book || order.Symbol != cur.Symbol || order.Side != cur.Side || order.Volume <= 0 || order.Volume > 1000000 || order.Filled < 0 || order.Filled > order.Volume {
			return out, fmt.Errorf("%w: inconsistent original order identity", ErrBadRequest)
		}
		if _, exists := orders[order.ClientOrderID]; exists {
			return out, fmt.Errorf("%w: duplicate order identity", ErrBadRequest)
		}
		orders[order.ClientOrderID] = order
		switch order.Status {
		case "rejected", "cancelled":
		case "filled":
			if order.Filled != order.Volume {
				return out, fmt.Errorf("%w: filled order quantity mismatch", ErrBadRequest)
			}
		case "pending_confirm", "submitted", "partial", "submitting", "cancel_pending", "unknown":
			active = true
		default:
			return out, fmt.Errorf("%w: unknown order state", ErrBadRequest)
		}
	}
	byOrder := map[string]int{}
	fillIDs := map[int]bool{}
	qty, amount := 0, 0.0
	for _, fill := range facts.Fills {
		_, found := orders[fill.ClientOrderID]
		if !found || fill.ID <= 0 || fillIDs[fill.ID] || fill.Book != cur.Book || fill.Symbol != cur.Symbol || fill.Side != cur.Side || fill.Qty <= 0 || fill.Price <= 0 || math.IsNaN(fill.Price) || math.IsInf(fill.Price, 0) || fill.Amount <= 0 || math.IsNaN(fill.Amount) || math.IsInf(fill.Amount, 0) || math.Abs(fill.Amount-fill.Price*float64(fill.Qty)) > 0.02 {
			return out, fmt.Errorf("%w: inconsistent authoritative fill", ErrBadRequest)
		}
		fillIDs[fill.ID] = true
		byOrder[fill.ClientOrderID] += fill.Qty
		qty += fill.Qty
		amount += fill.Amount
	}
	for id, order := range orders {
		if byOrder[id] != order.Filled {
			return out, fmt.Errorf("%w: order/fill quantities not yet reconciled", ErrExecutionProtected)
		}
	}
	if cur.ExecutedVolume != nil && qty < *cur.ExecutedVolume {
		return out, fmt.Errorf("%w: executed volume regressed", ErrExecutionProtected)
	}
	if qty > 0 {
		average := math.Round(amount/float64(qty)*10000) / 10000 // trade_signals numeric(12,4)
		out.ExecutedVolume, out.ExecutedPrice = &qty, &average
		out.Reason = "权威成交账本归并：已有实际成交，数量与成交金额加权均价已核对"
		switch cur.Status {
		case StatusPending, StatusRiskChecking, StatusApproved, StatusExecuting, StatusDone:
			out.Status = StatusDone
		case StatusCancelled, StatusExpired, StatusRejected:
			// This corrects a late authoritative fill, and cannot place an order.
			// The prior terminal status remains in the transactional event trail.
			out.Status = StatusDone
			out.Reason = "权威迟到成交纠正：原终结状态不能抹除实际成交；仅更新事实，不重发委托"
		case StatusClosed:
			return ExecutionProjection{Status: cur.Status}, nil // Closed-trade allocation/PnL is a separate contract.
		default:
			return out, fmt.Errorf("%w: unknown signal state", ErrBadRequest)
		}
		return out, nil
	}
	if cur.Status == StatusDone || cur.Status == StatusClosed {
		return out, fmt.Errorf("%w: completed signal has no authoritative fills", ErrExecutionProtected)
	}
	if cur.Status.Terminal() {
		return out, nil
	}
	if active || facts.UnresolvedIntent {
		out.Status = StatusExecuting
		out.Reason = "权威委托账本归并：委托处理中；待确认、UNKNOWN或撤单中不代表成交或终结"
	} else {
		out.Status = StatusCancelled
		out.Reason = "权威委托账本归并：全部原委托已终结且成交数量为零"
	}
	return out, nil
}

func (uc *Usecase) ReconcileExecutionOrder(ctx context.Context, clientOrderID string) (bool, error) {
	repo, ok := uc.signals.(ExecutionProjectionRepo)
	if !ok {
		return false, fmt.Errorf("strategy: execution projection repository unavailable")
	}
	id, err := repo.ResolveExecutionSignal(ctx, clientOrderID)
	if err != nil || id == 0 {
		return false, err
	}
	_, changed, err := repo.ProjectExecution(ctx, id, planExecution, executionEnvelope(ctx))
	return changed, err
}

// ReconcileExecutionsPage advances through bounded IDs even when one signal
// fails, so a broken historic record cannot starve recovery of later signals.
func (uc *Usecase) ReconcileExecutionsPage(ctx context.Context, after, limit int) (next, changed int, err error) {
	repo, ok := uc.signals.(ExecutionProjectionRepo)
	if !ok {
		return after, 0, fmt.Errorf("strategy: execution projection repository unavailable")
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	ids, err := repo.ExecutionCandidates(ctx, after, limit)
	if err != nil {
		return after, 0, err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return next, changed, errors.Join(err, ctx.Err())
		}
		next = id
		_, updated, e := repo.ProjectExecution(ctx, id, planExecution, executionEnvelope(ctx))
		if e != nil {
			err = errors.Join(err, fmt.Errorf("signal %d: %w", id, e))
			continue
		}
		if updated {
			changed++
		}
	}
	if len(ids) < limit {
		next = 0
	}
	return next, changed, err
}

func executionEnvelope(ctx context.Context) EnvelopeFunc {
	return func(s *Signal, from Status) (events.Envelope, error) {
		base, err := signalEnvelope(s, from)
		if err != nil {
			return base, err
		}
		env, err := events.NewWithContext(ctx, eventSource, events.SubjectSignal, base.Payload)
		if env.TraceID == "" {
			env.TraceID = s.TraceID
		}
		env.EntityID = fmt.Sprint(s.ID)
		return env, err
	}
}
