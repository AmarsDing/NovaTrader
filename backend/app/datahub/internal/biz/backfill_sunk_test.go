package biz

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

// D10 零行数据页终态提醒：sunk 登记 / 同日去重与失败停环 / 窗口形状 / 确认回执与重查。

type fakeUnlockSrc struct{ name string }

func (f *fakeUnlockSrc) Name() string { return f.name }
func (f *fakeUnlockSrc) UnlockPlans(context.Context, time.Time, time.Time) ([]UnlockPlan, error) {
	return []UnlockPlan{{Symbol: "000001.SZ", FreeDate: time.Now()}}, nil
}

type fakeBlockSrc struct {
	name string
	rows []BlockTrade
}

func (f *fakeBlockSrc) Name() string { return f.name }
func (f *fakeBlockSrc) BlockTrades(context.Context, time.Time) ([]BlockTrade, error) {
	return f.rows, nil
}

type sunkRepo struct {
	Repo
	issues   []Issue
	created  []BackfillJob
	pending  []BackfillJob
	succeeded []BackfillJob
	rows     int
	rowsErr  error
}

func (r *sunkRepo) SaveIssues(_ context.Context, issues []Issue) error {
	r.issues = append(r.issues, issues...)
	return nil
}

func (r *sunkRepo) PendingBackfills(context.Context) ([]BackfillJob, error) { return r.pending, nil }

func (r *sunkRepo) CreateBackfill(_ context.Context, job BackfillJob) (BackfillJob, error) {
	job.ID = len(r.created) + 1
	job.CreatedAt = time.Now()
	r.created = append(r.created, job)
	r.pending = append(r.pending, job)
	return job, nil
}

func (r *sunkRepo) ListBackfills(_ context.Context, domain string, statuses []string, since time.Time) ([]BackfillJob, error) {
	var out []BackfillJob
	match := map[string]bool{}
	for _, s := range statuses {
		match[s] = true
	}
	for _, j := range append(append([]BackfillJob{}, r.pending...), r.succeeded...) {
		if domain != "" && j.Domain != domain {
			continue
		}
		if len(match) > 0 && !match[j.Status] {
			continue
		}
		if !since.IsZero() && j.CreatedAt.Before(since) {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}

func (r *sunkRepo) DomainRows(context.Context, string, time.Time, time.Time) (int, error) {
	return r.rows, r.rowsErr
}

func testSunkCollector(t *testing.T, repo *sunkRepo) *Collector {
	t.Helper()
	cfg := Config{
		AllowUnofficial: true,
		Sources: map[string]SourceConfig{
			SourceEastmoney: {Enabled: true, Official: true, Timeout: time.Second},
			SourceTushare:   {Enabled: true, Official: true, Timeout: time.Second},
		},
		Domains: map[string]DomainConfig{
			DomainUnlock:     {Enabled: true, Sources: []string{SourceEastmoney}},
			DomainBlockTrade: {Enabled: true, Sources: []string{SourceTushare}},
		},
	}
	p, warn := NewPipeline(cfg, []Source{&fakeUnlockSrc{name: SourceEastmoney}, &fakeBlockSrc{name: SourceTushare}}, nil)
	if len(warn) > 0 {
		t.Fatalf("warnings %v", warn)
	}
	return NewCollector(p, repo, nil, nil, nil, nil, Options{}, log.DefaultLogger)
}

func TestSunkEmptyRegistersWindowJobAndDeduplicates(t *testing.T) {
	repo := &sunkRepo{}
	c := testSunkCollector(t, repo)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai())
	run := CollectSummary{Domain: DomainUnlock, Source: SourceEastmoney, Rows: 0}

	if !c.SunkEmptyBackfill(context.Background(), DomainUnlock, day, run) {
		t.Fatal("unlock 空运行应登记 sunk")
	}
	if len(repo.created) != 1 {
		t.Fatalf("created %d", len(repo.created))
	}
	job := repo.created[0]
	if job.RequestedBy != "sunk:unlock" || job.Source != "" || job.Status != JobPending {
		t.Fatalf("job %+v", job)
	}
	if !job.Start.Equal(day) || !job.End.Equal(day.AddDate(0, 0, 89)) {
		t.Fatalf("窗口 %v..%v（应为整窗 %v..%v）", job.Start, job.End, day, day.AddDate(0, 0, 89))
	}
	if job.Total < 60 {
		t.Fatalf("total %d（预期 90 天窗口交易日数）", job.Total)
	}
	var warned bool
	for _, is := range repo.issues {
		if is.Check == CheckEmpty && is.Severity == "warn" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("应落 CheckEmpty warn：%+v", repo.issues)
	}

	// 同日重复运行不重复登记。
	if c.SunkEmptyBackfill(context.Background(), DomainUnlock, day, run) {
		t.Fatal("同日重复运行不应再登记")
	}
	if len(repo.created) != 1 {
		t.Fatalf("dup: created %d", len(repo.created))
	}
	// 有行数不登记。
	if c.SunkEmptyBackfill(context.Background(), DomainUnlock, day, CollectSummary{Domain: DomainUnlock, Rows: 5}) {
		t.Fatal("rows>0 不应登记")
	}
	// BackfillCovered：当日/窗口中点均被覆盖；域外不覆盖。
	for _, probe := range []time.Time{day, day.AddDate(0, 0, 10)} {
		if ok, err := c.BackfillCovered(context.Background(), DomainUnlock, probe); err != nil || !ok {
			t.Fatalf("covered(%v)=%v %v", probe, ok, err)
		}
	}
	if ok, _ := c.BackfillCovered(context.Background(), DomainBlockTrade, day); ok {
		t.Fatal("域外不应覆盖")
	}
}

func TestSunkEmptyBlockTradeDailyJobAndFailedStop(t *testing.T) {
	repo := &sunkRepo{}
	c := testSunkCollector(t, repo)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai())
	run := CollectSummary{Domain: DomainBlockTrade, Rows: 0}

	if !c.SunkEmptyBackfill(context.Background(), DomainBlockTrade, day, run) {
		t.Fatal("block_trade 空运行应登记 sunk")
	}
	job := repo.created[0]
	if job.RequestedBy != "sunk:block_trade" || !job.Start.Equal(day) || !job.End.Equal(day) {
		t.Fatalf("job %+v（按日域 start=end=day）", job)
	}

	// 同日失败结果在册 → 不再登记（失败即结果，按日节奏）。
	day2 := day.AddDate(0, 0, 1)
	repo.succeeded = append(repo.succeeded, BackfillJob{Domain: DomainBlockTrade, Status: JobFailed,
		RequestedBy: "sunk:block_trade", CreatedAt: day2.Add(2 * time.Hour), Start: day2, End: day2})
	repo2 := &sunkRepo{succeeded: repo.succeeded}
	c2 := testSunkCollector(t, repo2)
	if c2.SunkEmptyBackfill(context.Background(), DomainBlockTrade, day2, CollectSummary{Domain: DomainBlockTrade, Rows: 0}) {
		t.Fatal("同日已失败不应再登记")
	}
}

func TestConfirmSunkBackfillsRetryTerminatesAndRearms(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, shanghai())
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai())
	sunk2DaysAgo := BackfillJob{ID: 7, Domain: DomainBlockTrade, Status: JobSuccess,
		RequestedBy: "sunk:block_trade", CreatedAt: now.AddDate(0, 0, -2), Start: day, End: day}

	// 已确认（窗口行数>0）：不登记重查。
	repoOK := &sunkRepo{succeeded: []BackfillJob{sunk2DaysAgo}, rows: 3}
	cOK := testSunkCollector(t, repoOK)
	cOK.now = func() time.Time { return now }
	cOK.ConfirmSunkBackfills(context.Background())
	if len(repoOK.created) != 0 {
		t.Fatalf("已确认不应登记重查：%+v", repoOK.created)
	}

	// 仍 0 行：必填域登记 retry（按域按日单条），附 warn；非必填 unlock 终止周期。
	repo0 := &sunkRepo{succeeded: []BackfillJob{sunk2DaysAgo}, rows: 0}
	c0 := testSunkCollector(t, repo0)
	c0.now = func() time.Time { return now }
	c0.ConfirmSunkBackfills(context.Background())
	if len(repo0.created) != 1 || repo0.created[0].RequestedBy != "retry:block_trade:20261020" {
		t.Fatalf("retry 登记 %+v", repo0.created)
	}
	// 再轮询：active retry 已存在 → 不重复。
	c0.ConfirmSunkBackfills(context.Background())
	if len(repo0.created) != 1 {
		t.Fatalf("重查应单条：%+v", repo0.created)
	}
	var warned bool
	for _, is := range repo0.issues {
		if is.Check == CheckEmpty && is.Severity == "warn" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("重查应附 CheckEmpty warn：%+v", repo0.issues)
	}

	// unlock 完成仍空 → 终止周期（不登记 retry）。
	unlockDone := BackfillJob{ID: 9, Domain: DomainUnlock, Status: JobSuccess,
		RequestedBy: "sunk:unlock", CreatedAt: now.AddDate(0, 0, -1), Start: day, End: day.AddDate(0, 0, 89)}
	repoUn := &sunkRepo{succeeded: []BackfillJob{unlockDone}, rows: 0}
	cUn := testSunkCollector(t, repoUn)
	cUn.now = func() time.Time { return now }
	cUn.ConfirmSunkBackfills(context.Background())
	if len(repoUn.created) != 0 {
		t.Fatalf("unlock 终止周期不应登记：%+v", repoUn.created)
	}

	// 读口错误=未知 → 必填域同样回防登记（绝不把未知冒充已回补）。
	repoErr := &sunkRepo{succeeded: []BackfillJob{sunk2DaysAgo}, rowsErr: fmt.Errorf("读口缺失")}
	cErr := testSunkCollector(t, repoErr)
	cErr.now = func() time.Time { return now }
	cErr.ConfirmSunkBackfills(context.Background())
	if len(repoErr.created) != 1 {
		t.Fatalf("读口未知必填域应登记重查：%+v", repoErr.created)
	}
}
