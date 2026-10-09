package data

import (
	"context"

	"server/pkg/events"
)

// Source 是 risk 发出事件的信封来源。
const Source = "risk"

// Publisher 把载荷装进信封直接发到 NATS，不经 outbox。
type Publisher struct {
	bus *events.Bus
}

func NewPublisher(bus *events.Bus) *Publisher { return &Publisher{bus: bus} }

func (p *Publisher) Publish(ctx context.Context, subject string, payload any) error {
	env, err := events.New(Source, subject, events.TraceID(ctx), payload)
	if err != nil {
		return err
	}
	return p.bus.Publish(ctx, env)
}
