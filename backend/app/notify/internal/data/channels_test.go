package data

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"server/app/notify/internal/biz"
	"server/conf"
	"server/pkg/events"
)

type capturePublisher struct{ envs []events.Envelope }

func (c *capturePublisher) Publish(_ context.Context, env events.Envelope) error {
	c.envs = append(c.envs, env)
	return nil
}

// feishu 回归：appcfg overlay 的 webhook 必须盖过 boot 配置值——channels.go 曾把
// 合并后的局部变量算出来却仍用 f.webhook 建请求（M13-T07① 修复钉死）。
func TestFeishuSendHonorsAppcfgOverlay(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil || m["msg_type"] != "text" {
			t.Errorf("bad feishu body: %q err %v", raw, err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 假 appcfg：只覆盖 webhook（secret UI 键不参与断言，返回空走 boot 值）。
	overlay := func(_ context.Context, key string) string {
		if key == "app.notify.webhook" {
			return srv.URL
		}
		return ""
	}
	dead := &http.Client{Timeout: 200 * time.Millisecond}

	// 1. boot webhook 是死地址、overlay 给活地址：消息必须送达活地址。
	f := NewFeishu(&conf.Notify{FeishuWebhook: "https://127.0.0.1:9/dead"})
	f.client, f.conf = dead, overlay
	if err := f.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("overlay send: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("overlay webhook must receive the message, hits=%d", hits.Load())
	}

	// 2. overlay 返回空白（trim 后为空）时回落 boot 配置：向活地址发成功。
	f2 := NewFeishu(&conf.Notify{FeishuWebhook: srv.URL})
	f2.client, f2.conf = srv.Client(), overlay
	if err := f2.Send(context.Background(), "boot-path"); err != nil {
		t.Fatalf("boot fallback send: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("boot webhook fallback must receive the message, hits=%d", hits.Load())
	}

	// 3. 合并后 webhook 仍为空：跳过不算失败。
	f3 := NewFeishu(nil)
	f3.conf = func(context.Context, string) string { return "" }
	if err := f3.Send(context.Background(), "x"); !errors.Is(err, biz.ErrSkipped) {
		t.Fatalf("empty merged webhook must skip, got %v", err)
	}
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
