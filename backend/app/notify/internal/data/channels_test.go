package data

import (
	"context"
	"testing"

	"server/app/notify/internal/biz"
	"server/pkg/events"
)

type capturePublisher struct{ envs []events.Envelope }

func (c *capturePublisher) Publish(_ context.Context, env events.Envelope) error {
	c.envs = append(c.envs, env)
	return nil
}

// 直发链：ctx 里有真实父信封（runner 消费 Handle 时放入）时，notify.desktop
// 续上同一链号与直接因果父——告警→通知自此沿同一 trace 可查。
func TestDesktopPushStitchesParentEnvelope(t *testing.T) {
	parent, err := events.New("risk", "risk.alert", "trace-risk-7", map[string]any{"k": 1})
	if err != nil {
		t.Fatal(err)
	}
	pub := &capturePublisher{}
	d := &Desktop{bus: pub}
	note := biz.DesktopNote{Title: "risk", TraceID: parent.TraceID}
	if err := d.Push(events.ContextWithEnvelope(context.Background(), parent), note); err != nil {
		t.Fatal(err)
	}
	if len(pub.envs) != 1 {
		t.Fatalf("envs=%d", len(pub.envs))
	}
	env := pub.envs[0]
	if env.Source != "notify" || env.Subject != events.SubjectNotifyDesktop {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if env.TraceID != parent.TraceID {
		t.Fatalf("trace not stitched: %q want %q", env.TraceID, parent.TraceID)
	}
	if env.CausationID != parent.EventID {
		t.Fatalf("causation not stitched: %q want %q", env.CausationID, parent.EventID)
	}
}

// 无父路径（无父直发、静默后 Flush 重投）：链号仍以行内 note.TraceID 为准。
// Flush 触发 ctx 顶多带「触发事件自己的链号」——那既不是父也不是本行链号，
// 绝不能顶掉行内链号，也绝不能编 causation_id。
func TestDesktopPushWithoutParentKeepsNoteTrace(t *testing.T) {
	pub := &capturePublisher{}
	d := &Desktop{bus: pub}
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		trace string
	}{
		{"no-parent-row-trace", context.Background(), "trace-row-1"},
		{"no-parent-empty-row-trace", context.Background(), ""},
		{"flush-trigger-trace-not-authoritative", events.WithTraceID(context.Background(), "trace-trigger-3"), "trace-row-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(pub.envs)
			if err := d.Push(tc.ctx, biz.DesktopNote{Title: "x", TraceID: tc.trace}); err != nil {
				t.Fatal(err)
			}
			env := pub.envs[before]
			if env.TraceID != tc.trace {
				t.Fatalf("note trace must stay authoritative: %q want %q", env.TraceID, tc.trace)
			}
			if env.CausationID != "" {
				t.Fatalf("no real parent means no causation: %+v", env)
			}
		})
	}
}
