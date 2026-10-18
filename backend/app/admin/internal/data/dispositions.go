package data

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"

	"server/app/admin/internal/gateway"
	"server/ent"
	"server/ent/eventdisposition"
)

// 处置留痕（N2-02 处置回执）：只标注 outbox 持久事件，不改投递状态、不回放。
func (s *AuditStore) ListDispositions(ctx context.Context, eventID, traceID string, limit int) ([]gateway.Disposition, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("database unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	q := s.client.EventDisposition.Query()
	if eventID != "" {
		q = q.Where(eventdisposition.EventID(eventID))
	}
	if traceID != "" {
		q = q.Where(eventdisposition.TraceID(traceID))
	}
	rows, err := q.Order(eventdisposition.ByCreatedAt(sql.OrderDesc()), eventdisposition.ByID(sql.OrderDesc())).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.Disposition, 0, len(rows))
	for _, r := range rows {
		out = append(out, toDisposition(r))
	}
	return out, nil
}

func (s *AuditStore) WriteDisposition(ctx context.Context, d gateway.Disposition) error {
	if !s.Enabled() {
		return fmt.Errorf("database unavailable")
	}
	_, err := s.client.EventDisposition.Create().
		SetEventID(d.EventID).SetTraceID(d.TraceID).SetAction(d.Action).
		SetNote(d.Note).SetActor(d.Actor).
		Save(ctx)
	return err
}

func toDisposition(r *ent.EventDisposition) gateway.Disposition {
	return gateway.Disposition{
		ID:        r.ID,
		EventID:   r.EventID,
		TraceID:   r.TraceID,
		Action:    r.Action,
		Note:      r.Note,
		Actor:     r.Actor,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}
