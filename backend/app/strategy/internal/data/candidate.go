package data

import (
	"context"
	"time"

	"server/app/strategy/internal/biz"
	"server/ent"
	"server/ent/strategycandidate"

	"entgo.io/ent/dialect/sql"
)

type candidateRepo struct{ d *Data }

func NewCandidateRepo(d *Data) biz.CandidateRepo { return &candidateRepo{d: d} }

var stageRank = map[string]int{biz.StageRanked: 1, biz.StageAIScored: 2, biz.StageSelected: 3}

func (r *candidateRepo) Save(ctx context.Context, list []biz.Candidate) error {
	if len(list) == 0 {
		return nil
	}
	tx, err := r.d.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range list {
		old, err := tx.StrategyCandidate.Query().
			Where(strategycandidate.TradeDate(c.TradeDate), strategycandidate.Strategy(c.Strategy), strategycandidate.StockCode(c.Symbol)).
			Only(ctx)
		if ent.IsNotFound(err) {
			if err := tx.StrategyCandidate.Create().
				SetTradeDate(c.TradeDate).SetStrategy(c.Strategy).SetNillableStrategyVersionID(c.VersionID).
				SetStockCode(c.Symbol).SetStockName(c.Name).SetPool(c.Pool).SetStage(c.Stage).
				SetMissReason(c.MissReason).SetRuleScore(c.RuleScore).
				SetNillableAiScore(c.AIScore).SetNillableFinalScore(c.FinalScore).
				SetNillableSignalID(c.SignalID).SetRefPrice(c.RefPrice).
				Exec(ctx); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if old.Stage == biz.StageSelected {
			continue
		}
		u := tx.StrategyCandidate.UpdateOneID(old.ID).
			SetRuleScore(c.RuleScore).SetMissReason(c.MissReason).SetNillableStrategyVersionID(c.VersionID)
		if stageRank[c.Stage] > stageRank[old.Stage] {
			u.SetStage(c.Stage)
		}
		if c.AIScore != nil {
			u.SetAiScore(*c.AIScore)
		}
		if c.FinalScore != nil {
			u.SetFinalScore(*c.FinalScore)
		}
		if c.Stage == biz.StageSelected {
			u.SetNillableSignalID(c.SignalID).SetRefPrice(c.RefPrice)
		}
		if err := u.Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *candidateRepo) PoolSymbols(ctx context.Context, day time.Time, pool string) ([]string, error) {
	return r.d.Client.StrategyCandidate.Query().
		Where(strategycandidate.TradeDate(day), strategycandidate.Pool(pool)).
		Unique(true).
		Select(strategycandidate.FieldStockCode).
		Strings(ctx)
}

func (r *candidateRepo) PendingOutcomes(ctx context.Context, since, today time.Time) ([]biz.Candidate, error) {
	rows, err := r.d.Client.StrategyCandidate.Query().
		Where(strategycandidate.TradeDateGTE(since), strategycandidate.TradeDateLT(today), strategycandidate.RetT5IsNil()).
		Order(strategycandidate.ByID()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return toCandidates(rows), nil
}

func toCandidates(rows []*ent.StrategyCandidate) []biz.Candidate {
	out := make([]biz.Candidate, len(rows))
	for i, row := range rows {
		out[i] = biz.Candidate{
			ID: row.ID, TradeDate: localDate(row.TradeDate), Strategy: row.Strategy, VersionID: row.StrategyVersionID,
			Symbol: row.StockCode, Name: row.StockName, Pool: row.Pool, Stage: row.Stage,
			MissReason: row.MissReason, RuleScore: row.RuleScore, AIScore: row.AiScore,
			FinalScore: row.FinalScore, SignalID: row.SignalID, RefPrice: row.RefPrice,
			RetT1: row.RetT1, RetT3: row.RetT3, RetT5: row.RetT5,
		}
	}
	return out
}

func (r *candidateRepo) List(ctx context.Context, f biz.CandidateFilter) ([]biz.Candidate, error) {
	q := r.d.Client.StrategyCandidate.Query()
	if !f.Day.IsZero() {
		q.Where(strategycandidate.TradeDate(f.Day))
	}
	if f.Pool != "" {
		q.Where(strategycandidate.Pool(f.Pool))
	}
	if f.Stage != "" {
		q.Where(strategycandidate.Stage(f.Stage))
	}
	rows, err := q.Order(
		strategycandidate.ByFinalScore(sql.OrderDesc(), sql.OrderNullsLast()),
		strategycandidate.ByRuleScore(sql.OrderDesc()),
		strategycandidate.ByID(),
	).Limit(f.Limit).All(ctx)
	if err != nil {
		return nil, err
	}
	return toCandidates(rows), nil
}

func (r *candidateRepo) SetOutcome(ctx context.Context, id int, t1, t3, t5 *float64) error {
	return r.d.Client.StrategyCandidate.UpdateOneID(id).
		SetNillableRetT1(t1).SetNillableRetT3(t3).SetNillableRetT5(t5).
		Exec(ctx)
}
