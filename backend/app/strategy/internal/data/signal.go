package data

import (
	"context"
	"fmt"
	"time"

	"server/app/strategy/internal/biz"
	"server/ent"
	"server/ent/tradesignal"
	"server/pkg/outbox"

	"entgo.io/ent/dialect/sql"
)

type signalRepo struct{ d *Data }

func NewSignalRepo(d *Data) biz.SignalRepo { return &signalRepo{d: d} }

func statusStrings(list []biz.Status) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = string(s)
	}
	return out
}

func toSignal(r *ent.TradeSignal) *biz.Signal {
	s := &biz.Signal{
		ID: r.ID, Book: r.Book, Time: r.SignalTime, Symbol: r.StockCode, Name: r.StockName,
		Side: string(r.SignalType), Strategy: r.Strategy, VersionID: r.StrategyVersionID,
		Entry: r.EntryPrice, StopLoss: r.StopLoss, TakeProfit: r.TakeProfit,
		PositionPct: r.PositionPct, HoldDaysMax: r.HoldDaysMax, RuleScore: r.RuleScore,
		AIScore: r.AiScore, FinalScore: r.ConfidenceScore, Dims: r.Dims, Evidence: r.Evidence,
		AIDegraded: r.AiDegraded, HighValue: r.HighValue, ExitKind: r.ExitKind,
		Reason: r.SignalReasoning, Status: biz.Status(r.Status), TraceID: r.TraceID,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.TradeDate != nil {
		s.TradeDate = localDate(*r.TradeDate)
	}
	if r.EntryLow != nil {
		s.EntryLow = *r.EntryLow
	}
	if r.EntryHigh != nil {
		s.EntryHigh = *r.EntryHigh
	}
	if r.ValidUntil != nil {
		s.ValidUntil = *r.ValidUntil
	}
	if r.Atr != nil {
		s.ATR = *r.Atr
	}
	s.ExitPrice, s.PnlPct, s.HoldingDays = r.ExitPrice, r.PnlPct, r.HoldingDays
	if r.ClosedAt != nil {
		s.ClosedAt = *r.ClosedAt
	}
	return s
}

func (r *signalRepo) Create(ctx context.Context, s *biz.Signal, env biz.EnvelopeFunc) (int, error) {
	tx, err := r.d.Client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	c := tx.TradeSignal.Create().
		SetBook(s.Book).SetSignalTime(s.Time).SetStockCode(s.Symbol).SetStockName(nameOr(s.Name, s.Symbol)).
		SetSignalType(tradesignal.SignalType(s.Side)).
		SetEntryPrice(s.Entry).SetStopLoss(s.StopLoss).SetTakeProfit(s.TakeProfit).
		SetSignalReasoning(s.Reason).SetConfidenceScore(s.FinalScore).SetStatus(string(s.Status)).
		SetStrategy(s.Strategy).SetNillableStrategyVersionID(s.VersionID).
		SetEntryLow(s.EntryLow).SetEntryHigh(s.EntryHigh).SetPositionPct(s.PositionPct).
		SetHoldDaysMax(s.HoldDaysMax).SetRuleScore(s.RuleScore).SetNillableAiScore(s.AIScore).
		SetAiDegraded(s.AIDegraded).SetHighValue(s.HighValue).SetExitKind(s.ExitKind).SetTraceID(s.TraceID)
	if !s.TradeDate.IsZero() {
		c.SetTradeDate(s.TradeDate)
	}
	if !s.ValidUntil.IsZero() {
		c.SetValidUntil(s.ValidUntil)
	}
	if s.ATR > 0 {
		c.SetAtr(s.ATR)
	}
	if s.Dims != nil {
		c.SetDims(s.Dims)
	}
	if s.Evidence != nil {
		c.SetEvidence(s.Evidence)
	}
	row, err := c.Save(ctx)
	if err != nil {
		return 0, err
	}
	saved := toSignal(row)
	if err := writeEvent(ctx, tx, saved, "", "创建："+s.Reason, biz.ActorStrategy, s.TraceID, env); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.ID, s.CreatedAt, s.UpdatedAt = saved.ID, saved.CreatedAt, saved.UpdatedAt
	return saved.ID, nil
}

func nameOr(name, symbol string) string {
	if name != "" {
		return name
	}
	return symbol
}

func writeEvent(ctx context.Context, tx *ent.Tx, s *biz.Signal, from biz.Status, reason, actor, trace string, env biz.EnvelopeFunc) error {
	if err := tx.SignalEvent.Create().
		SetSignalID(s.ID).SetFromStatus(string(from)).SetToStatus(string(s.Status)).
		SetReason(reason).SetActor(actor).SetTraceID(trace).
		Exec(ctx); err != nil {
		return fmt.Errorf("signal event: %w", err)
	}
	e, err := env(s, from)
	if err != nil {
		return err
	}
	return outbox.Insert(ctx, tx, e)
}

func (r *signalRepo) Transition(ctx context.Context, id int, from, to biz.Status, reason, actor, trace string, env biz.EnvelopeFunc, patch *biz.SignalPatch) (*biz.Signal, error) {
	tx, err := r.d.Client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	u := tx.TradeSignal.Update().
		Where(tradesignal.ID(id), tradesignal.Status(string(from))).
		SetStatus(string(to))
	if patch != nil {
		u.SetNillableExitPrice(patch.ExitPrice).SetNillablePnlPct(patch.PnlPct).
			SetNillableClosedAt(patch.ClosedAt).SetNillableHoldingDays(patch.HoldingDays)
	}
	n, err := u.Save(ctx)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, biz.ErrConflict
	}
	row, err := tx.TradeSignal.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	s := toSignal(row)
	if err := writeEvent(ctx, tx, s, from, reason, actor, trace, env); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s, nil
}

func (r *signalRepo) Get(ctx context.Context, id int) (*biz.Signal, error) {
	row, err := r.d.Client.TradeSignal.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, biz.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return toSignal(row), nil
}

func (r *signalRepo) Latest(ctx context.Context, book, symbol, side string) (*biz.Signal, error) {
	row, err := r.d.Client.TradeSignal.Query().
		Where(tradesignal.Book(book), tradesignal.StockCode(symbol), tradesignal.SignalTypeEQ(tradesignal.SignalType(side))).
		Order(tradesignal.ByCreatedAt(sql.OrderDesc()), tradesignal.ByID(sql.OrderDesc())).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toSignal(row), nil
}

func (r *signalRepo) HasOpen(ctx context.Context, book, symbol, side string) (bool, error) {
	return r.d.Client.TradeSignal.Query().
		Where(tradesignal.Book(book), tradesignal.StockCode(symbol),
			tradesignal.SignalTypeEQ(tradesignal.SignalType(side)),
			tradesignal.StatusIn(statusStrings(biz.OpenStatuses)...)).
		Exist(ctx)
}

func (r *signalRepo) CountBuys(ctx context.Context, book string, day time.Time) (int, error) {
	return r.d.Client.TradeSignal.Query().
		Where(tradesignal.Book(book), tradesignal.TradeDate(day), tradesignal.SignalTypeEQ(tradesignal.SignalTypeBuy)).
		Count(ctx)
}

func (r *signalRepo) Due(ctx context.Context, now time.Time) ([]*biz.Signal, error) {
	rows, err := r.d.Client.TradeSignal.Query().
		Where(tradesignal.StatusIn(statusStrings(biz.ExpirableStatuses)...), tradesignal.ValidUntilLT(now)).
		Order(tradesignal.ByID()).
		Limit(500).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.Signal, len(rows))
	for i, row := range rows {
		out[i] = toSignal(row)
	}
	return out, nil
}

func (r *signalRepo) List(ctx context.Context, f biz.SignalFilter) ([]*biz.Signal, error) {
	q := r.d.Client.TradeSignal.Query()
	if f.Book != "" {
		q.Where(tradesignal.Book(f.Book))
	}
	if !f.Day.IsZero() {
		q.Where(tradesignal.TradeDate(f.Day))
	}
	if f.Status != "" {
		q.Where(tradesignal.Status(string(f.Status)))
	}
	if f.Side != "" {
		q.Where(tradesignal.SignalTypeEQ(tradesignal.SignalType(f.Side)))
	}
	rows, err := q.Order(tradesignal.ByID(sql.OrderDesc())).Limit(f.Limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.Signal, len(rows))
	for i, row := range rows {
		out[i] = toSignal(row)
	}
	return out, nil
}

func (r *signalRepo) LastDoneBuy(ctx context.Context, book, symbol string) (*biz.Signal, error) {
	row, err := r.d.Client.TradeSignal.Query().
		Where(tradesignal.Book(book), tradesignal.StockCode(symbol),
			tradesignal.SignalTypeEQ(tradesignal.SignalTypeBuy), tradesignal.Status(string(biz.StatusDone))).
		Order(tradesignal.ByCreatedAt(sql.OrderDesc()), tradesignal.ByID(sql.OrderDesc())).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toSignal(row), nil
}
