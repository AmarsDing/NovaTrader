// Package outbox 在业务事务里记下待发事件，再由 Dispatch 投递到 NATS。
package outbox

import (
	"context"
	"fmt"
	"time"

	"server/ent"
	"server/ent/outbox"
	"server/pkg/events"

	"entgo.io/ent/dialect/sql"
)

// Publisher 是 Dispatch 使用的发送端口。*events.Bus 可以直接传入。
type Publisher interface {
	Publish(ctx context.Context, env events.Envelope) error
}

// Insert 把事件写入当前事务。调用方负责提交。
func Insert(ctx context.Context, tx *ent.Tx, env events.Envelope) error {
	if env.EventID == "" || env.Subject == "" || env.Source == "" {
		return fmt.Errorf("outbox: incomplete envelope")
	}
	payload := []byte(env.Payload)
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	if err := tx.Outbox.Create().
		SetEventID(env.EventID).
		SetTraceID(env.TraceID).
		SetSubject(env.Subject).
		SetSource(env.Source).
		SetPayload(payload).
		Exec(ctx); err != nil {
		return fmt.Errorf("outbox: insert: %w", err)
	}
	return nil
}

// Dispatcher 把 outbox 投递从常驻循环里隔开，core 不必持有 *ent.Client。
type Dispatcher struct {
	client *ent.Client
	bus    *events.Bus
}

// NewDispatcher 供 Wire 注入。bus 为空时 Dispatch 直接返回。
func NewDispatcher(client *ent.Client, bus *events.Bus) *Dispatcher {
	return &Dispatcher{client: client, bus: bus}
}

// Dispatch 发送尚未投递的事件。
func (d *Dispatcher) Dispatch(ctx context.Context, limit int) (int, error) {
	if d == nil || d.client == nil || d.bus == nil {
		return 0, nil
	}
	return Dispatch(ctx, d.client, d.bus, limit)
}

// Dispatch 发送尚未投递的事件。发送失败时停下，已成功的行会写下 published_at。
func Dispatch(ctx context.Context, client *ent.Client, pub Publisher, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := client.Outbox.Query().
		Where(outbox.PublishedAtIsNil()).
		Order(outbox.ByCreatedAt(sql.OrderAsc())).
		Limit(limit).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("outbox: query: %w", err)
	}
	sent := 0
	for _, row := range rows {
		env := events.Envelope{
			EventID: row.EventID,
			TraceID: row.TraceID,
			Time:    row.CreatedAt.UTC(),
			Source:  row.Source,
			Subject: row.Subject,
			Payload: row.Payload,
		}
		if err := pub.Publish(ctx, env); err != nil {
			msg := err.Error()
			if len(msg) > 500 {
				msg = msg[:500]
			}
			_ = client.Outbox.UpdateOneID(row.ID).AddAttempts(1).SetLastError(msg).Exec(ctx)
			return sent, err
		}
		if err := client.Outbox.UpdateOneID(row.ID).SetPublishedAt(time.Now()).ClearLastError().Exec(ctx); err != nil {
			return sent, fmt.Errorf("outbox: mark published: %w", err)
		}
		sent++
	}
	return sent, nil
}
