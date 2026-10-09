package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	duplicateWindow = 2 * time.Hour
	streamMaxAge    = 7 * 24 * time.Hour
)

// Bus 把持久化主题发到 JetStream，把行情主题发到普通 NATS。
type Bus struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

// Dial 连接 NATS，并确保业务流存在。url 为空时用 nats://127.0.0.1:4222。
func Dial(url string) (*Bus, error) {
	if url == "" {
		url = nats.DefaultURL
	}
	nc, err := nats.Connect(url, nats.Timeout(2*time.Second), nats.Name("novatrader-events"))
	if err != nil {
		return nil, fmt.Errorf("events: connect: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("events: jetstream: %w", err)
	}
	if err := EnsureStream(js); err != nil {
		nc.Close()
		return nil, err
	}
	return &Bus{nc: nc, js: js}, nil
}

func (b *Bus) Close() {
	if b != nil && b.nc != nil {
		b.nc.Close()
	}
}

// EnsureStream 创建 NOVATRADER 流。已存在时只补上缺少的主题，其余设置不动。
func EnsureStream(js nats.JetStreamContext) error {
	info, err := js.StreamInfo(StreamName)
	if err == nil {
		missing := missingSubjects(info.Config.Subjects, StreamSubjects())
		if len(missing) == 0 {
			return nil
		}
		cfg := info.Config
		cfg.Subjects = append(append([]string(nil), cfg.Subjects...), missing...)
		if _, err := js.UpdateStream(&cfg); err != nil {
			return fmt.Errorf("events: update stream subjects: %w", err)
		}
		return nil
	}
	if !errors.Is(err, nats.ErrStreamNotFound) {
		return fmt.Errorf("events: stream info: %w", err)
	}
	_, err = js.AddStream(&nats.StreamConfig{
		Name:       StreamName,
		Subjects:   StreamSubjects(),
		Storage:    nats.FileStorage,
		Retention:  nats.LimitsPolicy,
		Duplicates: duplicateWindow,
		MaxAge:     streamMaxAge,
	})
	if err != nil {
		return fmt.Errorf("events: add stream: %w", err)
	}
	return nil
}

func missingSubjects(have, want []string) []string {
	set := make(map[string]struct{}, len(have))
	for _, s := range have {
		set[s] = struct{}{}
	}
	var out []string
	for _, s := range want {
		if _, ok := set[s]; !ok {
			out = append(out, s)
		}
	}
	return out
}

// JetStream 返回底层上下文，供消费端建持久订阅。
func (b *Bus) JetStream() nats.JetStreamContext {
	return b.js
}

// Conn 返回底层连接，供订阅 market.* 这类不进流的主题。
func (b *Bus) Conn() *nats.Conn {
	return b.nc
}

// Publish 发送一条已经编好号的事件。同一 event_id 在去重窗口内再次发送会被 JetStream 丢掉。
func (b *Bus) Publish(ctx context.Context, env Envelope) error {
	body, err := env.Marshal()
	if err != nil {
		return err
	}
	if !Persistent(env.Subject) {
		if err := b.nc.Publish(env.Subject, body); err != nil {
			return fmt.Errorf("events: publish %s: %w", env.Subject, err)
		}
		return nil
	}
	if _, err := b.js.Publish(env.Subject, body, nats.MsgId(env.EventID), nats.Context(ctx)); err != nil {
		return fmt.Errorf("events: jetstream publish %s: %w", env.Subject, err)
	}
	return nil
}
