package data

import (
	"context"
	"fmt"
	"strconv"

	"server/app/strategy/internal/biz"
	"server/ent"
	"server/ent/decisionplan"
	"server/ent/executionintent"
	"server/ent/tradefill"
	"server/ent/tradeorder"
	"server/ent/tradesignal"
	"server/pkg/events"
)

func (r *signalRepo) ResolveExecutionSignal(ctx context.Context, clientOrderID string) (int, error) {
	row, err := r.d.client.TradeOrder.Query().Where(tradeorder.ClientOrderID(clientOrderID)).Only(ctx)
	if ent.IsNotFound(err) {
		return 0, biz.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(row.SignalID)
	if err != nil || id <= 0 {
		return 0, nil
	} // Manual/protective orders may have no strategy signal.
	return id, nil
}

func executionFacts(ctx context.Context, c *ent.Client, s *biz.Signal) (biz.ExecutionFacts, error) {
	f := biz.ExecutionFacts{}
	rows, err := c.TradeOrder.Query().Where(tradeorder.Book(s.Book), tradeorder.SignalID(strconv.Itoa(s.ID))).Limit(1001).All(ctx)
	if err != nil {
		return f, err
	}
	if len(rows) > 1000 {
		return f, fmt.Errorf("strategy: signal execution orders exceed safe bound")
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ClientOrderID)
		f.Orders = append(f.Orders, biz.ExecutionOrder{ClientOrderID: row.ClientOrderID, Book: row.Book, Symbol: row.Symbol, Side: row.Side, Status: row.Status, Volume: row.Volume, Filled: row.Filled})
	}
	{
		identity := tradefill.SignalID(strconv.Itoa(s.ID))
		if len(ids) > 0 {
			identity = tradefill.Or(identity, tradefill.ClientOrderIDIn(ids...))
		}
		fills, err := c.TradeFill.Query().Where(identity).Limit(10001).All(ctx)
		if err != nil {
			return f, err
		}
		if len(fills) > 10000 {
			return f, fmt.Errorf("strategy: signal execution fills exceed safe bound")
		}
		for _, row := range fills {
			// Check the fill's own association too; an order ID is not permission
			// to silently adopt a contradictory signal identity.
			if row.SignalID != strconv.Itoa(s.ID) {
				return f, fmt.Errorf("%w: fill signal identity mismatch", biz.ErrBadRequest)
			}
			f.Fills = append(f.Fills, biz.ExecutionFill{ID: row.ID, ClientOrderID: row.ClientOrderID, Book: row.Book, Symbol: row.Symbol, Side: row.Side, Qty: row.Qty, Price: row.Price, Amount: row.Amount})
		}
	}
	f.UnresolvedIntent, err = unresolvedSignalIntent(ctx, c, s)
	return f, err
}

func unresolvedSignalIntent(ctx context.Context, c *ent.Client, s *biz.Signal) (bool, error) {
	account := "SIM"
	if s.Book == "live" {
		account = "LIVE"
	}
	key := fmt.Sprintf("signal:%s:default:%d", account, s.ID)
	plans, err := c.DecisionPlan.Query().Where(decisionplan.Book(s.Book), decisionplan.SignalID(s.ID)).Select(decisionplan.FieldPlanID).Strings(ctx)
	if err != nil {
		return false, err
	}
	identity := executionintent.BusinessKey(key)
	if len(plans) > 0 {
		identity = executionintent.Or(identity, executionintent.PlanIDIn(plans...))
	}
	return c.ExecutionIntent.Query().Where(executionintent.Book(s.Book), executionintent.StateIn("claimed", "unknown"), identity).Exist(ctx)
}

func protectsExecution(ctx context.Context, c *ent.Client, s *biz.Signal) (bool, error) {
	orders, err := c.TradeOrder.Query().Where(tradeorder.Book(s.Book), tradeorder.SignalID(strconv.Itoa(s.ID))).Exist(ctx)
	if err != nil || orders {
		return orders, err
	}
	return unresolvedSignalIntent(ctx, c, s)
}

func (r *signalRepo) ProjectExecution(ctx context.Context, id int, planner biz.ExecutionPlanner, env biz.EnvelopeFunc) (*biz.Signal, bool, error) {
	tx, err := r.d.client.Tx(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	row, err := tx.TradeSignal.Query().Where(tradesignal.ID(id)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, false, biz.ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	cur := toSignal(row)
	facts, err := executionFacts(ctx, tx.Client(), cur)
	if err != nil {
		return nil, false, err
	}
	projection, err := planner(cur, facts)
	if err != nil {
		return nil, false, err
	}
	progressChanged := projection.ExecutedVolume != nil && (cur.ExecutedVolume == nil || *cur.ExecutedVolume != *projection.ExecutedVolume) || projection.ExecutedPrice != nil && (cur.ExecutedPrice == nil || *cur.ExecutedPrice != *projection.ExecutedPrice)
	if projection.Status == cur.Status && !progressChanged {
		return cur, false, nil
	}
	u := tx.TradeSignal.UpdateOneID(id).SetStatus(string(projection.Status))
	if projection.ExecutedVolume != nil {
		u.SetExecutedVolume(*projection.ExecutedVolume)
	}
	if projection.ExecutedPrice != nil {
		u.SetExecutedPrice(*projection.ExecutedPrice)
	}
	row, err = u.Save(ctx)
	if err != nil {
		return nil, false, err
	}
	next := toSignal(row)
	trace := events.TraceID(ctx)
	if trace == "" {
		trace = next.TraceID
	}
	if err = writeEvent(ctx, tx, next, cur.Status, projection.Reason, biz.ActorTrade, trace, env); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return next, true, nil
}

func (r *signalRepo) ExecutionCandidates(ctx context.Context, after, limit int) ([]int, error) {
	// Keyset pages cover terminal rows too: contradictory late fills must surface
	// for reconciliation instead of disappearing merely because a signal ended.
	return r.d.client.TradeSignal.Query().Where(tradesignal.IDGT(after)).Order(tradesignal.ByID()).Limit(limit).IDs(ctx)
}

var _ biz.ExecutionProjectionRepo = (*signalRepo)(nil)
