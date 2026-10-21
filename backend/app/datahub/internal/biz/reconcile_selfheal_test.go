package biz

import (
	"context"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

// D2 对账自修复钩：登记 / 滤域 / N 日计数 / 停环与复位。

type selfhealRepo struct {
	Repo
	issues  []Issue
	created []BackfillJob
	pending []BackfillJob
}

func (r *selfhealRepo) SaveIssues(_ context.Context, issues []Issue) error {
	r.issues = append(r.issues, issues...)
	return nil
}

func (r *selfhealRepo) PendingBackfills(context.Context) ([]BackfillJob, error) { return r.pending, nil }

func (r *selfhealRepo) CreateBackfill(_ context.Context, job BackfillJob) (BackfillJob, error) {
	r.created = append(r.created, job)
	// 与生产 persistence 一致：落成 pending 后下一次 PendingBackfills 可见（同日复跑去重靠它）。
	r.pending = append(r.pending, job)
	return job, nil
}

type captureAlerter struct {
	list []string
}

func (a *captureAlerter) Alert(_ context.Context, level, title, body string) {
	a.list = append(a.list, level+"|"+title+"|"+body)
}

type fakeDaily struct {
	name string
}

func (f *fakeDaily) Name() string { return f.name }

func (f *fakeDaily) DailyBars(context.Context, BarQuery) (BarBatch, error) { return BarBatch{}, nil }

// testSelfhealCollector 只经过自修复钩内部路径（不经 Reconcile 的对照源对账），
// 用最小域配置让 SubmitBackfill 校验通过。
func testSelfhealCollector(t *testing.T, repo *selfhealRepo, alerter *captureAlerter) *Collector {
	t.Helper()
	cfg := Config{
		AllowUnofficial: true,
		Sources:         map[string]SourceConfig{SourceTdxLocal: {Enabled: true, Official: true, Timeout: time.Second}},
		Domains:         map[string]DomainConfig{DomainDailyBar: {Enabled: true, Sources: []string{SourceTdxLocal}}},
	}
	p, warn := NewPipeline(cfg, []Source{&fakeDaily{name: SourceTdxLocal}}, nil)
	if len(warn) > 0 {
		t.Fatalf("warnings %v", warn)
	}
	// NewCollector 对 nil 日历用 tradecal.Default 兜底（覆盖 2025–2026，测试日都是交易日）。
	return NewCollector(p, repo, nil, nil, nil, alerter, Options{}, log.DefaultLogger)
}

func diffIssues(symbols ...string) []Issue {
	out := make([]Issue, 0, len(symbols))
	for _, s := range symbols {
		out = append(out, Issue{Domain: DomainDailyBar, Check: CheckReconcile, Symbol: s, Severity: "warn",
			Source: "tdx_local", OtherSource: "tushare", Detail: map[string]any{"close": []float64{10, 10.5}}})
	}
	return out
}

func TestSelfHealRegistersScopedBackfill(t *testing.T) {
	repo := &selfhealRepo{}
	c := testSelfhealCollector(t, repo, nil)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai())
	issues := append(diffIssues("300750.SZ", "000001.SZ"),
		// 对照源缺根（Detail 空）不应回补：那是源缺数不是入库错（没取到≠0）。
		Issue{Domain: DomainDailyBar, Check: CheckReconcile, Symbol: "600519.SH", Severity: "warn", Message: "tushare 没有这根日线"},
	)
	c.selfHealReconcile(context.Background(), DomainDailyBar, day, issues, 0)
	if len(repo.created) != 1 {
		t.Fatalf("created %d jobs", len(repo.created))
	}
	job := repo.created[0]
	if job.Domain != DomainDailyBar || job.Source != "" || job.Status != JobPending {
		t.Fatalf("job %+v", job)
	}
	if !job.Start.Equal(day) || !job.End.Equal(day) {
		t.Fatalf("range %v..%v", job.Start, job.End)
	}
	if len(job.Symbols) != 2 || job.Symbols[0] != "000001.SZ" || job.Symbols[1] != "300750.SZ" {
		t.Fatalf("symbols %v（应排序去重且滤掉无差异标的）", job.Symbols)
	}
	if job.Total != 1 {
		t.Fatalf("total %d", job.Total)
	}

	// 同一天重复对账：计数不翻倍，pending 去重后不重复登记。
	c.selfHealReconcile(context.Background(), DomainDailyBar, day, issues, 0)
	if len(repo.created) != 1 {
		t.Fatalf("same-day rerun created %d jobs", len(repo.created))
	}
}

func TestSelfHealSkipsNonBackfillableDomain(t *testing.T) {
	repo := &selfhealRepo{}
	c := testSelfhealCollector(t, repo, nil)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai())
	for _, domain := range []string{DomainSnapshot, DomainHotRank, DomainOverseas, "intel"} {
		c.selfHealReconcile(context.Background(), domain, day, diffIssues("000001.SZ"), 0)
	}
	if len(repo.created) != 0 {
		t.Fatalf("non-backfillable domain created %d jobs", len(repo.created))
	}
	// SelfHealDays<0 全关。
	c.selfHealReconcile(context.Background(), DomainDailyBar, day, diffIssues("000001.SZ"), -1)
	if len(repo.created) != 0 {
		t.Fatalf("disabled selfheal created %d jobs", len(repo.created))
	}
}

func TestSelfHealStopAfterNDaysAndResetOnClean(t *testing.T) {
	repo := &selfhealRepo{}
	alerter := &captureAlerter{}
	c := testSelfhealCollector(t, repo, alerter)
	day0 := time.Date(2026, 10, 19, 0, 0, 0, 0, shanghai())
	syms := diffIssues("000001.SZ")

	// 第 1..3 日（默认上限 DefaultSelfHealDays=3）每日登记一单。
	for i := 0; i < defaultSelfHealDays; i++ {
		day := day0.AddDate(0, 0, i)
		c.selfHealReconcile(context.Background(), DomainDailyBar, day, syms, 0)
		if len(repo.created) != i+1 {
			t.Fatalf("day %d: created %d jobs", i+1, len(repo.created))
		}
	}
	// 第 4 日：升级 error + 停环，不再登记。
	day4 := day0.AddDate(0, 0, 3)
	c.selfHealReconcile(context.Background(), DomainDailyBar, day4, syms, 0)
	if len(repo.created) != defaultSelfHealDays {
		t.Fatalf("stopped day still created: %d", len(repo.created))
	}
	var escalated bool
	for _, is := range repo.issues {
		if is.Severity == "error" && is.Check == CheckReconcile {
			escalated = true
		}
	}
	if !escalated || len(alerter.list) != 1 {
		t.Fatalf("escalation issues=%d alerts=%v", len(repo.issues), alerter.list)
	}
	// 停环期间继续来差异：不登记、不重复告警。
	c.selfHealReconcile(context.Background(), DomainDailyBar, day0.AddDate(0, 0, 4), syms, 0)
	if len(repo.created) != defaultSelfHealDays || len(alerter.list) != 1 {
		t.Fatalf("stopped loop leaked: jobs=%d alerts=%d", len(repo.created), len(alerter.list))
	}
	// 干净日复位（转净），之后又出现差异 → 重新计数并允许再次升级。
	day6 := day0.AddDate(0, 0, 5)
	c.selfHealReconcile(context.Background(), DomainDailyBar, day6, nil, 0)
	for i := 0; i < defaultSelfHealDays; i++ {
		c.selfHealReconcile(context.Background(), DomainDailyBar, day6.AddDate(0, 0, i+1), syms, 0)
	}
	c.selfHealReconcile(context.Background(), DomainDailyBar, day6.AddDate(0, 0, defaultSelfHealDays+1), syms, 0)
	if len(alerter.list) != 2 {
		t.Fatalf("re-armed escalation alerts=%d", len(alerter.list))
	}
}

func TestSelfHealPendingDedupCoversSubset(t *testing.T) {
	repo := &selfhealRepo{
		pending: []BackfillJob{{
			Domain: DomainDailyBar, Status: JobPending,
			Start: time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai()), End: time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai()),
			Symbols: []string{"000001.SZ", "600519.SH"},
		}},
	}
	c := testSelfhealCollector(t, repo, nil)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai())

	// 全部被 pending 覆盖 → 不登记。
	c.selfHealReconcile(context.Background(), DomainDailyBar, day, diffIssues("000001.SZ", "600519.SH"), 0)
	if len(repo.created) != 0 {
		t.Fatalf("covered set created %d jobs", len(repo.created))
	}
	// 部分覆盖 → 只登记缺口标的（窗口按新计数推进，但去重以 pending 为准）。
	c.selfHealReconcile(context.Background(), DomainDailyBar, day, diffIssues("000001.SZ", "300750.SZ"), 0)
	if len(repo.created) != 1 || len(repo.created[0].Symbols) != 1 || repo.created[0].Symbols[0] != "300750.SZ" {
		t.Fatalf("gap registration %+v", repo.created)
	}
}
