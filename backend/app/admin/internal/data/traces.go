package data

import (
	"context"
	"encoding/json"
	"fmt"
	v1 "server/api/admin/v1"
	"server/ent"
	box "server/ent/outbox"
	"server/pkg/events"
	"time"
)

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func (s *AuditStore) ReadTrace(ctx context.Context, trace string, limit int) (*v1.GetTraceReply, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("database unavailable")
	}
	now := time.Now().UTC()
	out := &v1.GetTraceReply{SchemaVersion: 1, Available: true, Scope: "durable_outbox_history", AsOf: now.Format(time.RFC3339Nano), Events: []*v1.TraceEvent{}, MissingParentIds: []string{}, Note: "持久事件形成/投递历史；普通行情、业务消费者处理及ACK未被此查询覆盖。缺父事件表示本次范围未观测，不推测根因。"}
	pending, err := s.client.Outbox.Query().Where(box.PublishedAtIsNil(), box.DeadLetter(false)).Count(ctx)
	if err != nil {
		return nil, err
	}
	out.Pending = uint64(pending)
	leased, err := s.client.Outbox.Query().Where(box.PublishedAtIsNil(), box.LeaseUntilGT(now)).Count(ctx)
	if err != nil {
		return nil, err
	}
	out.Leased = uint64(leased)
	dead, err := s.client.Outbox.Query().Where(box.DeadLetter(true)).Count(ctx)
	if err != nil {
		return nil, err
	}
	out.DeadLetters = uint64(dead)
	old, err := s.client.Outbox.Query().Where(box.PublishedAtIsNil(), box.DeadLetter(false)).Order(box.ByCreatedAt()).First(ctx)
	if err == nil {
		out.OldestPendingAt = old.CreatedAt.UTC().Format(time.RFC3339Nano)
	} else if !ent.IsNotFound(err) {
		return nil, err
	}
	if trace == "" {
		return out, nil
	}
	rows, err := s.client.Outbox.Query().Where(box.TraceID(trace)).Order(box.ByCreatedAt(), box.ByID()).Limit(limit + 1).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Truncated = true
	}
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.EventID] = true
	}
	missing := map[string]bool{}
	for _, r := range rows {
		e := &v1.TraceEvent{EventId: r.EventID, TraceId: r.TraceID, Source: r.Source, Subject: r.Subject, FormedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano), PublishedAt: stamp(r.PublishedAt), Attempts: int32(r.Attempts), NextAttemptAt: stamp(r.NextAttemptAt), LeaseUntil: stamp(r.LeaseUntil), State: "pending"}
		if r.PublishedAt != nil {
			e.State = "published"
		} else if r.DeadLetter {
			e.State = "dead_letter"
		} else if r.LeaseUntil != nil && r.LeaseUntil.After(now) {
			e.State = "leased"
		} else if r.NextAttemptAt != nil && r.NextAttemptAt.After(now) {
			e.State = "retry_wait"
		}
		// Decode only contract metadata. Raw business payload is never read or sent.
		b, err := json.Marshal(r.Metadata)
		if err != nil {
			return nil, err
		}
		var meta events.Envelope
		if err = json.Unmarshal(b, &meta); err == nil {
			e.CausationId = meta.CausationID
			e.EntityId = meta.EntityID
			e.EntityVersion = meta.EntityVersion
			e.SourceTime = stamp(meta.SourceTime)
		}
		if e.CausationId != "" && !ids[e.CausationId] {
			missing[e.CausationId] = true
		}
		if r.LastError != nil {
			switch *r.LastError {
			case "publisher unavailable", "invalid envelope metadata":
				e.ErrorClass = *r.LastError
			default:
				e.ErrorClass = "recorded failure"
			}
		}
		out.Events = append(out.Events, e)
	}
	for id := range missing {
		out.MissingParentIds = append(out.MissingParentIds, id)
	}
	return out, nil
}
