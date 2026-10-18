package biz

import (
	"context"
	"fmt"
	"strings"

	"server/pkg/decision"
)

// LedgerStore holds a database row lock over every ledger mutation, including
// risk checks and nested commits. The context carries the data-layer transaction.
type LedgerStore interface {
	InLedger(context.Context) bool
	FinishIntentDetached(context.Context, string, string, string) error
	WithLedger(context.Context, string, func(context.Context) error) error
	ClaimIntent(context.Context, Intent) (Intent, bool, error)
	FinishIntent(context.Context, string, string, string) error
}
type Intent struct {
	BusinessKey   string `json:"business_key"`
	ClientOrderID string `json:"client_order_id"`
	RequestHash   string `json:"request_hash"`
	Book          string `json:"book"`
	PlanID        string `json:"plan_id"`
	PlanRevision  int    `json:"plan_revision"`
	State         string `json:"state"`
	Reason        string `json:"reason"`
}

func (e *Engine) withLedger(ctx context.Context, book string, fn func(context.Context) error) error {
	if store, ok := e.store.(LedgerStore); ok {
		return store.WithLedger(ctx, book, fn)
	}
	return fmt.Errorf("%w: durable ledger unavailable", ErrInvalid)
}
func orderRequestHash(req PlaceRequest, book string) string {
	// Match bars are market inputs, never part of the immutable order identity.
	return decision.Hash(struct {
		Book, Symbol, Side, Source, Operator, Signal, Strategy, Plan string
		Price                                                        float64
		Volume, Revision                                             int
	}{book, strings.TrimSpace(req.Symbol), strings.ToLower(strings.TrimSpace(req.Side)), strings.ToLower(strings.TrimSpace(req.Source)), req.Operator, req.SignalID, req.StrategyVersion, req.PlanID, req.Price, req.Volume, req.PlanRevision})
}
func (e *Engine) placeDurable(ctx context.Context, req PlaceRequest) (Order, error) {
	book, err := bookOf(req.Account)
	if err != nil {
		return Order{}, err
	}
	req.ClientOrderID = strings.TrimSpace(req.ClientOrderID)
	if req.ClientOrderID == "" || req.Price <= 0 || req.Volume <= 0 {
		return Order{}, ErrInvalid
	}
	if req.Source == "" {
		req.Source = "auto"
	}
	store, ok := e.store.(LedgerStore)
	if !ok {
		return Order{}, fmt.Errorf("%w: durable intent unavailable", ErrInvalid)
	}
	key := "order:" + req.ClientOrderID
	if strings.TrimSpace(req.SignalID) != "" {
		key = "signal:" + accountOf(book) + ":default:" + strings.TrimSpace(req.SignalID)
	}
	if req.BusinessKey != "" {
		key = req.BusinessKey
	}
	hash := orderRequestHash(req, book)
	if hash == "" {
		return Order{}, ErrInvalid
	}
	in, created, err := store.ClaimIntent(ctx, Intent{BusinessKey: key, ClientOrderID: req.ClientOrderID, RequestHash: hash, Book: book, PlanID: req.PlanID, PlanRevision: req.PlanRevision, State: "claimed"})
	if err != nil {
		return Order{}, err
	}
	if in.RequestHash != hash || in.ClientOrderID != req.ClientOrderID {
		return Order{}, fmt.Errorf("%w: 同一委托身份对应不同请求", ErrConflict)
	}
	if !created {
		o, found, err := e.store.LoadOrder(ctx, in.ClientOrderID)
		if err != nil {
			return Order{}, err
		}
		if found {
			return o, nil
		}
		return Order{}, fmt.Errorf("%w: 原意图 %s 状态 %s，需核原单", ErrUncertain, in.ClientOrderID, in.State)
	}
	var out Order
	err = e.withLedger(ctx, "global", func(txctx context.Context) error {
		var err error
		out, err = e.place(txctx, req)
		if err != nil {
			return err
		}
		return store.FinishIntent(txctx, in.ClientOrderID, out.Status, out.Reason)
	})
	if err != nil {
		// Never retry a risk-approved or commit-ambiguous intent with a new ID.
		if !store.InLedger(ctx) {
			_ = store.FinishIntentDetached(ctx, in.ClientOrderID, "unknown", "执行中断；仅核对原订单")
		}
		return Order{}, fmt.Errorf("%w: %w", ErrExecutionInterrupted, err)
	}
	return out, nil
}
