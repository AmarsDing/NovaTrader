// Package outbox 在业务事务里记下待发事件，再由 Dispatch 投递到 NATS。
package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"server/ent"

	"server/pkg/events"
)

// Publisher 是 Dispatch 使用的发送端口。*events.Bus 可以直接传入。
type Publisher interface {
	Publish(ctx context.Context, env events.Envelope) error
}

// Insert 把事件写入当前事务。调用方负责提交。
func Insert(ctx context.Context, tx *ent.Tx, env events.Envelope) error {
	meta, err := envelopeMetadata(env)
	if err != nil {
		return err
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
		SetMetadata(meta).
		SetPayload(payload).
		SetCreatedAt(env.Time).
		Exec(ctx); err != nil {
		return fmt.Errorf("outbox: insert: %w", err)
	}
	return nil
}

// envelopeMetadata 不重复保存大payload；保留事件形成时刻及所有可选身份/时钟。
func envelopeMetadata(env events.Envelope) (map[string]any, error) {
	if env.Time.IsZero() {
		return nil, fmt.Errorf("outbox: missing event time")
	}
	raw, err := env.Marshal()
	if err != nil {
		return nil, err
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	delete(meta, "payload")
	delete(meta, "published_at") // 每次实际发布尝试由Bus重新记录
	return meta, nil
}

func envelopeFromRow(row *ent.Outbox) (events.Envelope, error) {
	env := events.Envelope{}
	if len(row.Metadata) != 0 {
		raw, err := json.Marshal(row.Metadata)
		if err != nil {
			return env, fmt.Errorf("outbox: metadata: %w", err)
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return env, fmt.Errorf("outbox: metadata: %w", err)
		}
	}
	// 主列是稳定身份与payload真值。旧行没有metadata，继续读取legacy外壳。
	env.EventID, env.TraceID, env.Source, env.Subject = row.EventID, row.TraceID, row.Source, row.Subject
	env.Payload = row.Payload
	if env.Time.IsZero() {
		env.Time = row.CreatedAt.UTC()
	}
	if _, err := env.Marshal(); err != nil {
		return events.Envelope{}, err
	}
	return env, nil
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

// Dispatch leases one row per bounded publish attempt. Bad rows are isolated
// with retry/dead-letter state; a poison row does not block unrelated events.
func Dispatch(ctx context.Context, client *ent.Client, pub Publisher, limit int) (int, error) {
	return dispatchLeased(ctx, client, pub, limit)
}
