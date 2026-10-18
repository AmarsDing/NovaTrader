package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"server/app/trade/internal/biz"
	"server/ent"
	"server/ent/accountsnapshot"
	"server/ent/confighead"
	"server/ent/decisionplan"
	"server/ent/executionintent"
	"server/ent/tradesignal"
	"server/pkg/accountstate"
	"server/pkg/decision"
	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/outbox"
	"server/pkg/params"
)

func (r *Repo) PlanFacts(ctx context.Context, id int, book string) (biz.PlanFacts, error) {
	c := r.clientFor(ctx)
	sigq := c.TradeSignal.Query().Where(tradesignal.ID(id), tradesignal.Book(book))
	if ctx.Value(ledgerTxKey{}) != nil {
		sigq.ForShare()
	}
	s, err := sigq.Only(ctx)
	if ent.IsNotFound(err) {
		return biz.PlanFacts{}, biz.ErrNotFound
	}
	if err != nil {
		return biz.PlanFacts{}, err
	}
	if _, err = params.Read(ctx, c); err != nil {
		return biz.PlanFacts{}, err
	}
	hq := c.ConfigHead.Query().Where(confighead.ID(1))
	if ctx.Value(ledgerTxKey{}) != nil {
		hq.ForShare()
	}
	if _, err = hq.Only(ctx); err != nil {
		return biz.PlanFacts{}, err
	}
	config, err := params.Read(ctx, c)
	if err != nil {
		return biz.PlanFacts{}, err
	}
	row, err := c.AccountSnapshot.Query().Where(accountsnapshot.Book(book)).Order(ent.Desc(accountsnapshot.FieldID)).First(ctx)
	if err != nil {
		return biz.PlanFacts{}, err
	}
	account, err := accountstate.Decode(row.State)
	if err != nil {
		return biz.PlanFacts{}, fmt.Errorf("账户完整状态不可用: %w", err)
	}
	quote, err := r.planQuote(ctx, s.StockCode)
	if err != nil {
		return biz.PlanFacts{}, err
	}
	f := biz.PlanFacts{SignalID: s.ID, Book: s.Book, Symbol: s.StockCode, Side: string(s.SignalType), Strategy: s.Strategy, Status: s.Status, Reason: s.SignalReasoning, ConfigRevision: config.Revision, Entry: s.EntryPrice, StopLoss: s.StopLoss, TakeProfit: s.TakeProfit, PositionPct: s.PositionPct, RuleScore: s.RuleScore, ModelScore: s.AiScore, FormedAt: s.SignalTime, Evidence: s.Evidence, Account: account, Quote: quote}
	if s.EntryLow != nil {
		f.EntryLow = *s.EntryLow
	}
	if s.EntryHigh != nil {
		f.EntryHigh = *s.EntryHigh
	}
	if s.ValidUntil != nil {
		f.ValidUntil = *s.ValidUntil
	}
	if s.StrategyVersionID != nil {
		f.StrategyRevision = *s.StrategyVersionID
	}
	f.SignalHash = decision.Hash(struct {
		ID                                   int
		Book, Symbol, Side, Strategy, Status string
		Revision                             int
		Entry, Low, High, Stop, Target, Pct  float64
		Until                                time.Time
		Evidence                             []string
	}{s.ID, s.Book, s.StockCode, string(s.SignalType), s.Strategy, s.Status, f.StrategyRevision, f.Entry, f.EntryLow, f.EntryHigh, f.StopLoss, f.TakeProfit, f.PositionPct, f.ValidUntil, s.Evidence})
	return f, nil
}

func (r *Repo) planQuote(ctx context.Context, symbol string) (market.Snapshot, error) {
	if r.quote != nil {
		return r.quote(ctx, symbol)
	}
	if r.rdb == nil {
		return market.Snapshot{}, fmt.Errorf("实时行情未接入")
	}
	raw, err := r.rdb.HGet(ctx, market.SnapshotKey, symbol).Bytes()
	if err != nil {
		return market.Snapshot{}, fmt.Errorf("实时行情未就绪")
	}
	return market.ParseSnapshot(raw)
}
func planFrom(row *ent.DecisionPlan) (biz.PlanRecord, error) {
	var r biz.PlanRecord
	if err := json.Unmarshal(row.Snapshot, &r.Snapshot); err != nil {
		return r, err
	}
	if err := json.Unmarshal(row.Plan, &r.Plan); err != nil {
		return r, err
	}
	r.State = row.State
	r.Mode = row.Mode
	r.Operator = row.Operator
	r.Reason = row.Reason
	return r, r.Plan.Validate(r.Snapshot)
}
func (r *Repo) SavePlan(ctx context.Context, in biz.PlanRecord, base int) error {
	tx, commit, rollback, err := r.transaction(ctx)
	if err != nil {
		return err
	}
	defer rollback()
	if base > 0 {
		n, err := tx.DecisionPlan.Update().Where(decisionplan.PlanID(in.Plan.PlanID), decisionplan.Revision(base), decisionplan.StateIn("draft", "authorized", "revoked", "expired", "blocked")).SetState("superseded").Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return biz.ErrConflict
		}
	}
	a, err := json.Marshal(in.Snapshot)
	if err != nil {
		return err
	}
	b, err := json.Marshal(in.Plan)
	if err != nil {
		return err
	}
	if _, err = tx.DecisionPlan.Create().SetPlanID(in.Plan.PlanID).SetRevision(in.Plan.Revision).SetBook("paper").SetSignalID(in.Plan.SignalID).SetSnapshot(a).SetPlan(b).SetState(in.State).SetMode(in.Mode).Save(ctx); err != nil {
		if ent.IsConstraintError(err) {
			return biz.ErrConflict
		}
		return err
	}
	if err = planEvent(ctx, tx, in.Plan.PlanID, in.Plan.Revision, in.State); err != nil {
		return err
	}
	return commit()
}
func (r *Repo) GetPlan(ctx context.Context, id string, revision int) (biz.PlanRecord, error) {
	q := r.clientFor(ctx).DecisionPlan.Query().Where(decisionplan.PlanID(id))
	if revision > 0 {
		q.Where(decisionplan.Revision(revision))
	}
	q.Order(ent.Desc(decisionplan.FieldRevision))
	if ctx.Value(ledgerTxKey{}) != nil {
		q.ForUpdate()
	}
	row, err := q.First(ctx)
	if ent.IsNotFound(err) {
		return biz.PlanRecord{}, biz.ErrNotFound
	}
	if err != nil {
		return biz.PlanRecord{}, err
	}
	return planFrom(row)
}
func (r *Repo) ListPlans(ctx context.Context, book, state string) ([]biz.PlanRecord, error) {
	q := r.clientFor(ctx).DecisionPlan.Query().Where(decisionplan.Book(book))
	if state != "" {
		q.Where(decisionplan.State(state))
	}
	rows, err := q.Order(ent.Desc(decisionplan.FieldID)).Limit(200).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.PlanRecord, 0, len(rows))
	for _, row := range rows {
		p, err := planFrom(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func (r *Repo) SetPlanState(ctx context.Context, id string, rev int, from, to, mode, operator, reason string) error {
	tx, commit, rollback, err := r.transaction(ctx)
	if err != nil {
		return err
	}
	defer rollback()
	n, err := tx.DecisionPlan.Update().Where(decisionplan.PlanID(id), decisionplan.Revision(rev), decisionplan.State(from)).SetState(to).SetMode(mode).SetOperator(operator).SetReason(reason).SetUpdatedAt(time.Now()).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return biz.ErrConflict
	}
	if err = planEvent(ctx, tx, id, rev, to); err != nil {
		return err
	}
	return commit()
}
func planEvent(ctx context.Context, tx *ent.Tx, id string, rev int, state string) error {
	env, err := events.NewWithContext(ctx, "trade", "trade.plan", map[string]any{"version": 1, "plan_id": id, "revision": rev, "state": state, "book": "paper"})
	if err != nil {
		return err
	}
	env.EntityID = id
	env.EntityVersion = int64(rev)
	return outbox.Insert(ctx, tx, env)
}
func (r *Repo) ListIntents(ctx context.Context, book string) ([]biz.Intent, error) {
	rows, err := r.clientFor(ctx).ExecutionIntent.Query().Where(executionintent.Book(book)).Order(ent.Desc(executionintent.FieldID)).Limit(200).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Intent, 0, len(rows))
	for _, row := range rows {
		out = append(out, intentFrom(row))
	}
	return out, nil
}

func (r *Repo) ListUnresolvedIntents(ctx context.Context, book string) ([]biz.Intent, error) {
	rows, err := r.clientFor(ctx).ExecutionIntent.Query().Where(executionintent.Book(book), executionintent.StateIn("claimed", "unknown")).Order(ent.Asc(executionintent.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Intent, 0, len(rows))
	for _, row := range rows {
		out = append(out, intentFrom(row))
	}
	return out, nil
}
