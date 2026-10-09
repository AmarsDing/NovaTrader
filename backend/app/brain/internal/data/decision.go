package data

import (
	"context"
	"fmt"

	"server/app/brain/internal/biz"
	"server/ent"
)

const agentName = "brain"

type decisionRepo struct {
	db *ent.Client
}

func NewDecisionRepo(db *ent.Client) biz.DecisionRepo {
	return &decisionRepo{db: db}
}

func (r *decisionRepo) Begin(ctx context.Context, kind, symbol, traceID string) (int, error) {
	row, err := r.db.AgentDecision.Create().
		SetAgentName(agentName).
		SetDecisionType(kind).
		SetSymbol(symbol).
		SetTraceID(traceID).
		Save(ctx)
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}

func (r *decisionRepo) Finish(ctx context.Context, id int, rec biz.DecisionRecord) error {
	return r.db.AgentDecision.UpdateOneID(id).
		SetContent(rec.Content).
		SetNillableConfidence(rec.Confidence).
		SetPromptVersion(clip(rec.PromptVersion, 64)).
		SetModel(clip(rec.Model, 128)).
		SetAiDegraded(rec.Degraded).
		SetDiscarded(rec.Discarded).
		SetLatencyMs(rec.LatencyMS).
		Exec(ctx)
}

func (r *decisionRepo) Get(ctx context.Context, id int) (*biz.DecisionRecord, error) {
	row, err := r.db.AgentDecision.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, fmt.Errorf("%w: 研判记录 %d", biz.ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	return &biz.DecisionRecord{
		Kind: derefStr(row.DecisionType), Symbol: row.Symbol, TraceID: row.TraceID,
		Content: row.Content, Confidence: row.Confidence,
		PromptVersion: row.PromptVersion, Model: row.Model,
		Degraded: row.AiDegraded, Discarded: row.Discarded, LatencyMS: row.LatencyMs,
	}, nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
