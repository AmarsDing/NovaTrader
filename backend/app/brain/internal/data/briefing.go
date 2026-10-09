package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"server/app/brain/internal/biz"
	"server/ent"
	"server/ent/morningbriefing"
	"server/pkg/events"
	"server/pkg/outbox"

	entsql "entgo.io/ent/dialect/sql"
)

type briefingRepo struct {
	db *ent.Client
}

func NewBriefingRepo(db *ent.Client) biz.BriefingRepo {
	return &briefingRepo{db: db}
}

type briefingEvent struct {
	Date       string `json:"date"`
	Degraded   bool   `json:"degraded"`
	Headline   string `json:"headline"`
	DecisionID int    `json:"decision_id"`
}

// Save 覆盖当天的晨报，并在同一事务里写 strategy.briefing 事件。
func (r *briefingRepo) Save(ctx context.Context, b *biz.Briefing) error {
	summary := map[string]any{}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		return err
	}
	env, err := events.New("brain", events.SubjectBriefing, events.TraceID(ctx), briefingEvent{
		Date: b.Date.Format("2006-01-02"), Degraded: b.Degraded, Headline: b.Headline, DecisionID: b.DecisionID,
	})
	if err != nil {
		return err
	}
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n, err := tx.MorningBriefing.Update().
		Where(morningbriefing.DateEQ(b.Date)).
		SetSummary(summary).
		SetCreatedAt(b.CreatedAt).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("update briefing: %w", err)
	}
	if n == 0 {
		if err := tx.MorningBriefing.Create().
			SetDate(b.Date).
			SetSummary(summary).
			SetCreatedAt(b.CreatedAt).
			Exec(ctx); err != nil {
			return fmt.Errorf("create briefing: %w", err)
		}
	}
	if err := outbox.Insert(ctx, tx, env); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *briefingRepo) Get(ctx context.Context, day time.Time) (*biz.Briefing, error) {
	row, err := r.db.MorningBriefing.Query().
		Where(morningbriefing.DateEQ(day)).
		Order(morningbriefing.ByCreatedAt(entsql.OrderDesc())).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(row.Summary)
	if err != nil {
		return nil, err
	}
	var b biz.Briefing
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("decode briefing: %w", err)
	}
	b.Date = day
	return &b, nil
}
