package biz

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// 运行时盖写（app.ds.*/app.domain.*）行为钉死。注入缝：Pipeline.get 生产为 appcfg.Get，
// 这里注入 map；Run 头部三个 apply* 每次按当前 map 重放盖写，空串=清盖写回落 yaml。

func mapGet(m map[string]string) func(context.Context, string) string {
	return func(_ context.Context, key string) string { return m[key] }
}

func overridePipe(t *testing.T, srcs []Source, sourceCfgs map[string]SourceConfig, chain []string) *Pipeline {
	t.Helper()
	cfg := Config{
		AllowUnofficial: true,
		BreakerFailures: 3,
		BreakerCooldown: time.Minute,
		Sources:         sourceCfgs,
		Domains:         map[string]DomainConfig{DomainSnapshot: {Enabled: true, Sources: chain}},
	}
	p, warns := NewPipeline(cfg, srcs, nil)
	if len(warns) > 0 {
		t.Fatalf("warnings %v", warns)
	}
	return p
}

func snapFetch(ctx context.Context, s Source) (*SnapshotBatch, error) {
	x, err := s.(SnapshotSource).Snapshots(ctx, nil)
	return &x, err
}

func overrideSources(t *testing.T) (a, b *fakeSnap, p *Pipeline, gets map[string]string) {
	t.Helper()
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, shanghai())
	a = &fakeSnap{name: "sina", err: errors.New("down")}
	b = &fakeSnap{name: "tencent", items: []Snapshot{{Symbol: "600519.SH", Time: now, Last: 100}}}
	p = overridePipe(t, []Source{a, b}, map[string]SourceConfig{
		"sina":    {Enabled: true, Official: true, Timeout: time.Second},
		"tencent": {Enabled: true, Official: true, Timeout: time.Second},
	}, []string{"sina", "tencent"})
	p.now = func() time.Time { return now }
	gets = map[string]string{}
	p.get = mapGet(gets)
	return a, b, p, gets
}

// ①覆盖 ["tencent","sina"] 后首位即成功，sina 一次都不调用。
func TestPipelineOverrideChainReorder(t *testing.T) {
	a, _, p, gets := overrideSources(t)
	gets["app.domain."+DomainSnapshot+".sources"] = `["tencent","sina"]`
	_, src, err := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil)
	if err != nil || src != "tencent" {
		t.Fatalf("src=%s err=%v", src, err)
	}
	if a.calls != 0 {
		t.Fatalf("overridden tail source was called %d times", a.calls)
	}
}

// ②覆盖前 sina 已失败 2 次，换链后第 3 次失败即断路：断路器随持久 entry 跨换链连续。
func TestPipelineOverrideBreakerContinuity(t *testing.T) {
	_, _, p, gets := overrideSources(t)
	for i := 0; i < 2; i++ {
		if _, _, err := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	gets["app.domain."+DomainSnapshot+".sources"] = `["sina"]`
	if _, _, err := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil); err == nil {
		t.Fatal("single failing source must error")
	}
	state, failures := p.domains[DomainSnapshot].entriesAll["sina"].breaker.State()
	if state != StateOpen || failures != 3 {
		t.Fatalf("breaker state=%s failures=%d, want open/3", state, failures)
	}
	if _, _, err := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil); err == nil || !strings.Contains(err.Error(), "断路打开") {
		t.Fatalf("open breaker should short-circuit, err=%v", err)
	}
}

// ③清除盖写键后链回落 yaml 基线顺序。
func TestPipelineOverrideClearFallsBack(t *testing.T) {
	a, b, p, gets := overrideSources(t)
	a.err = nil // 两源都成功，观察首位源名即可读链序
	gets["app.domain."+DomainSnapshot+".sources"] = `["tencent","sina"]`
	if _, src, _ := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil); src != "tencent" {
		t.Fatalf("override first=%s, want tencent", src)
	}
	delete(gets, "app.domain."+DomainSnapshot+".sources")
	if _, src, _ := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil); src != "sina" {
		t.Fatalf("after clear first=%s, want yaml first sina", src)
	}
	if b.calls == 0 || a.calls == 0 {
		t.Fatalf("both sources should have served once: sina=%d tencent=%d", a.calls, b.calls)
	}
}

// ④链含未注册源或不支持该域的源时整条不落地、保持现链。
func TestPipelineOverrideInvalidChainKept(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, shanghai())
	good := &fakeSnap{name: "sina", items: []Snapshot{{Symbol: "600519.SH", Time: now, Last: 100}}}
	other := &nonSnapshotSource{name: "rss"} // 已注册但不实现 SnapshotSource
	p := overridePipe(t, []Source{good, other}, map[string]SourceConfig{
		"sina": {Enabled: true, Official: true, Timeout: time.Second},
	}, []string{"sina"})
	p.now = func() time.Time { return now }
	gets := map[string]string{}
	p.get = mapGet(gets)

	for _, bad := range []string{`["sina","ghost"]`, `["rss"]`, `not-json`} {
		gets["app.domain."+DomainSnapshot+".sources"] = bad
		if _, src, err := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil); err != nil || src != "sina" {
			t.Fatalf("chain %s: src=%s err=%v, chain must stay", bad, src, err)
		}
		cur := p.domains[DomainSnapshot].activeChain()
		if len(cur) != 1 || cur[0].state.src.Name() != "sina" {
			t.Fatalf("chain %s mutated active chain", bad)
		}
	}
}

// ⑤quota=1 盖写后第二次 Run 即报当日配额已用完；清除盖写回落 yaml（DailyQuota=0 不限）。
func TestPipelineOverrideQuotaExhausted(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, shanghai())
	good := &fakeSnap{name: "sina", items: []Snapshot{{Symbol: "600519.SH", Time: now, Last: 100}}}
	p := overridePipe(t, []Source{good}, map[string]SourceConfig{
		"sina": {Enabled: true, Official: true, Timeout: time.Second},
	}, []string{"sina"})
	p.now = func() time.Time { return now }
	gets := map[string]string{"app.ds.sina.quota": "1"}
	p.get = mapGet(gets)

	if _, _, err := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil); err != nil {
		t.Fatalf("first run within quota: %v", err)
	}
	if _, _, err := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil); err == nil || !strings.Contains(err.Error(), "当日配额已用完") {
		t.Fatalf("second run must report quota exhausted, err=%v", err)
	}
	delete(gets, "app.ds.sina.quota")
	if _, _, err := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil); err != nil {
		t.Fatalf("cleared quota override must fall back to yaml (unlimited): %v", err)
	}
}

// ⑥timeout_sec=1 盖写触发单次超时；清除后回落 cfg.Timeout 恢复。
func TestPipelineOverrideTimeoutAndClear(t *testing.T) {
	good := &fakeSnap{name: "sina"}
	p := overridePipe(t, []Source{good}, map[string]SourceConfig{
		"sina": {Enabled: true, Official: true, Timeout: 30 * time.Second},
	}, []string{"sina"})
	gets := map[string]string{"app.ds.sina.timeout_sec": "1"}
	p.get = mapGet(gets)

	slow := func(ctx context.Context, s Source) (*SnapshotBatch, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if _, _, err := Run(context.Background(), p, DomainSnapshot, RunOptions{}, slow, nil); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("timeout override must fire, err=%v", err)
	}
	delete(gets, "app.ds.sina.timeout_sec")
	good.items = []Snapshot{{Symbol: "600519.SH", Time: time.Now(), Last: 100}}
	if _, src, err := Run(context.Background(), p, DomainSnapshot, RunOptions{}, snapFetch, nil); err != nil || src != "sina" {
		t.Fatalf("cleared timeout override must restore cfg.Timeout: src=%s err=%v", src, err)
	}
}

// ⑦qps 盖写装配 Run 粒度节流器；清除后移除。同包私有断言 gap，不做墙钟时序断言防 CI 抖动。
func TestPipelineOverrideQPSRate(t *testing.T) {
	good := &fakeSnap{name: "sina"}
	p := overridePipe(t, []Source{good}, map[string]SourceConfig{
		"sina": {Enabled: true, Official: true, Timeout: time.Second},
	}, []string{"sina"})
	gets := map[string]string{"app.ds.sina.qps": "4"}
	p.get = mapGet(gets)

	applySourceLimits(context.Background(), p, p.get)
	st := p.sources["sina"]
	rate := st.rate.Load()
	if rate == nil {
		t.Fatal("qps override must install a Run-level limiter")
	}
	rate.mu.Lock()
	gap := rate.gap
	rate.mu.Unlock()
	if gap <= 0 {
		t.Fatalf("limiter gap=%v, want >0", gap)
	}
	delete(gets, "app.ds.sina.qps")
	applySourceLimits(context.Background(), p, p.get)
	if st.rate.Load() != nil {
		t.Fatal("cleared qps override must remove the limiter")
	}
}

type nonSnapshotSource struct{ name string }

func (s *nonSnapshotSource) Name() string { return s.name }
