package biz

import (
	"context"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"

	"server/pkg/events"
)

func TestOpenRunTraceOpensRespectsAndStaysFresh(t *testing.T) {
	// 无链 ctx：开新链，且是合法 uuid
	first, second := OpenRunTrace(context.Background()), OpenRunTrace(context.Background())
	t1, t2 := events.TraceID(first), events.TraceID(second)
	if t1 == "" || t2 == "" || t1 == t2 {
		t.Fatalf("fresh traces: %q %q", t1, t2)
	}
	if _, err := uuid.Parse(t1); err != nil {
		t.Fatalf("trace not a uuid: %q", t1)
	}
	// 已有链：尊重调用方串链，不另开
	if got := events.TraceID(OpenRunTrace(events.WithTraceID(context.Background(), "caller-chain"))); got != "caller-chain" {
		t.Fatalf("caller chain not respected: %q", got)
	}
	// 上游父信封的链：同样尊重（级联/回放触发透传）
	parent, err := events.New("intel", events.SubjectIntelAlert, "parent-trace-9", map[string]any{"k": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := events.TraceID(OpenRunTrace(events.ContextWithEnvelope(context.Background(), parent))); got != "parent-trace-9" {
		t.Fatalf("parent trace not respected: %q", got)
	}
}

type intelCursorRepo struct {
	Repo
	cursors map[string]string
}

func (r *intelCursorRepo) Cursor(_ context.Context, name string) (string, error) {
	return r.cursors[name], nil
}

func (r *intelCursorRepo) SetCursor(_ context.Context, name, value string) error {
	r.cursors[name] = value
	return nil
}

type fakeIntelSrc struct {
	calls int
	now   time.Time
}

func (f *fakeIntelSrc) Name() string { return "fakeintel" }

// 每次采集给唯一 SourceID：包级 intelSeen 按 domain|source|sourceID 去重，
// 测试里逐次换键模拟真实新情报，行为与生产一致。
func (f *fakeIntelSrc) Intel(context.Context, string, IntelQuery) ([]Intel, error) {
	f.calls++
	tag := string(rune('a' + f.calls))
	return []Intel{
		{Source: "fakeintel", SourceID: "rt-" + tag + "-1", Title: "宁德时代签订海外大单", PublishTime: f.now.UTC().Format(time.RFC3339)},
		{Source: "fakeintel", SourceID: "rt-" + tag + "-2", Title: "贵州茅台回购进展公告", PublishTime: f.now.UTC().Format(time.RFC3339)},
	}, nil
}

type captureBus struct{ envs []events.Envelope }

func (b *captureBus) Publish(_ context.Context, env events.Envelope) error {
	b.envs = append(b.envs, env)
	return nil
}

// RunDomain 一次运行开一条链：运行内逐条 intel.raw 共享同一链号；两次运行链号不同；
// 调用方已串链则尊重（调度/手工触发语义自此可查：采集→intel 评分→告警沿同一 trace）。
func TestRunDomainOpensRunTraceForEmittedIntel(t *testing.T) {
	now := time.Date(2026, 10, 19, 9, 30, 0, 0, shanghai())
	src := &fakeIntelSrc{now: now}
	p, warns := NewPipeline(Config{
		AllowUnofficial: true, BreakerFailures: 3, BreakerCooldown: time.Minute,
		Sources: map[string]SourceConfig{"fakeintel": {Enabled: true, Official: false, Timeout: time.Second}},
		Domains: map[string]DomainConfig{DomainNews: {Enabled: true, Sources: []string{"fakeintel"}}},
	}, []Source{src}, nil)
	if len(warns) > 0 {
		t.Fatal(warns)
	}
	bus := &captureBus{}
	c := NewCollector(p, &intelCursorRepo{cursors: map[string]string{}}, nil, bus, nil, nil, Options{}, log.DefaultLogger)
	c.now = func() time.Time { return now }

	res, err := c.RunDomain(context.Background(), DomainNews, "2026-10-19", "")
	if err != nil || res.Rows != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(bus.envs) != 2 {
		t.Fatalf("envs=%d", len(bus.envs))
	}
	trace := ""
	for _, e := range bus.envs {
		if e.Source != serviceName || e.Subject != events.SubjectIntelRaw(KindNews) {
			t.Fatalf("unexpected envelope: %+v", e)
		}
		if e.TraceID == "" {
			t.Fatalf("no run trace: %+v", e)
		}
		if trace == "" {
			trace = e.TraceID
		}
		if e.TraceID != trace {
			t.Fatalf("events in one run must share one trace: %q vs %q", trace, e.TraceID)
		}
		if e.CausationID != "" {
			t.Fatalf("run root must not invent a parent: %+v", e)
		}
	}

	// 第二次运行：新链，不沿用上一次
	res2, err := c.RunDomain(context.Background(), DomainNews, "2026-10-19", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = res2
	if len(bus.envs) != 4 {
		t.Fatalf("envs after second run=%d", len(bus.envs))
	}
	if bus.envs[2].TraceID == "" || bus.envs[2].TraceID == trace || bus.envs[2].TraceID != bus.envs[3].TraceID {
		t.Fatalf("second run must open a fresh shared trace: %q %q (first %q)",
			bus.envs[2].TraceID, bus.envs[3].TraceID, trace)
	}

	// 调用方已串链：运行尊重该链（级联触发不另开）
	if _, err := c.RunDomain(events.WithTraceID(context.Background(), "cascade-1"), DomainNews, "2026-10-19", ""); err != nil {
		t.Fatal(err)
	}
	for _, e := range bus.envs[4:] {
		if e.TraceID != "cascade-1" {
			t.Fatalf("caller chain not respected in run: %q", e.TraceID)
		}
	}
}
