package data

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"server/app/datahub/internal/biz"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

// D2 隔离 PG 集成：对账发现差异 → 自修复登记单日 scoped 回补 → 回补执行 → 复对账收敛。
// 与 biz 单测互补：全链路走真实 ent 仓储（market_bar/ready 归因、backfill_job、data_quality_issue）。
func TestReconcileSelfHealConvergesAfterBackfill(t *testing.T) {
	client := selfHealPG(t)
	repo := NewRepo(client)

	// 日线 Bar.Time 用上海时区零点，与生产采集器一致（trade_date 键按时区归日）。
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
	tdx := &flipDaily{name: biz.SourceTdxLocal, bars: []biz.Bar{
		{Symbol: "600519.SH", Time: day, Freq: "1d", Open: 10, High: 10.1, Low: 9.9, Close: 10, Volume: 100, Amount: 1000},
		{Symbol: "000001.SZ", Time: day, Freq: "1d", Open: 10, High: 10.1, Low: 9.9, Close: 10, Volume: 100, Amount: 1000},
	}}
	// 对照源对 000001.SZ 给出显著不同的收盘（0.5 元 >> 容差），600519.SH 完全一致。
	tushare := &flipDaily{name: biz.SourceTushare, bars: []biz.Bar{
		{Symbol: "600519.SH", Time: day, Freq: "1d", Open: 10, High: 10.1, Low: 9.9, Close: 10, Volume: 100, Amount: 1000},
		{Symbol: "000001.SZ", Time: day, Freq: "1d", Open: 10.5, High: 10.6, Low: 10.4, Close: 10.5, Volume: 100, Amount: 1000},
	}}
	cfg := biz.Config{
		AllowUnofficial: true,
		BreakerFailures: 3,
		BreakerCooldown: time.Minute,
		Sources: map[string]biz.SourceConfig{
			biz.SourceTdxLocal: {Enabled: true, Official: true, Timeout: 30 * time.Second},
			biz.SourceTushare:  {Enabled: true, Official: true, Timeout: 30 * time.Second},
		},
		Domains: map[string]biz.DomainConfig{
			biz.DomainDailyBar: {Enabled: true, Sources: []string{biz.SourceTdxLocal, biz.SourceTushare}},
		},
	}
	p, warn := biz.NewPipeline(cfg, []biz.Source{tdx, tushare}, nil)
	if len(warn) > 0 {
		t.Fatalf("warnings %v", warn)
	}
	col := biz.NewCollector(p, repo, nil, nil, nil, nil, biz.Options{}, log.DefaultLogger)
	ctx := context.Background()
	symbols := []string{"000001.SZ", "600519.SH"}

	if _, err := col.CollectDailyBars(ctx, day, symbols, biz.SourceTdxLocal); err != nil {
		t.Fatalf("collect: %v", err)
	}

	sum, err := col.Reconcile(ctx, day, biz.ReconcileOptions{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sum.Rows != 2 {
		t.Fatalf("checked %d", sum.Rows)
	}
	// 自修复登记：单日 scoped 回补，只含值不一致的 000001.SZ。
	pending, err := repo.PendingBackfills(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending %v err=%v", pending, err)
	}
	job := pending[0]
	if job.Domain != biz.DomainDailyBar || job.RequestedBy != "selfheal:reconcile" ||
		len(job.Symbols) != 1 || job.Symbols[0] != "000001.SZ" || job.Total != 1 {
		t.Fatalf("selfheal job %+v", job)
	}

	// 主源盘后修正到位，回补按基线链重采并覆盖入库值。
	tdx.bars[1].Open, tdx.bars[1].High, tdx.bars[1].Low, tdx.bars[1].Close = 10.5, 10.6, 10.4, 10.5
	done := col.RunBackfill(ctx, job)
	if done.Status != biz.JobSuccess {
		t.Fatalf("backfill %s: %s", done.Status, done.Error)
	}

	// 复对账收敛：无新差异、无新回补任务（自修复窗口按干净日复位）。
	sum2, err := col.Reconcile(ctx, day, biz.ReconcileOptions{})
	if err != nil {
		t.Fatalf("re-reconcile: %v", err)
	}
	if sum2.Rows != 2 {
		t.Fatalf("re-checked %d", sum2.Rows)
	}
	pending2, err := repo.PendingBackfills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending2) != 0 {
		t.Fatalf("loop not converged: %+v", pending2)
	}
	issues, err := repo.ListIssues(ctx, &day, biz.DomainDailyBar, 10)
	if err != nil {
		t.Fatal(err)
	}
	warnDiffs := 0
	for _, is := range issues {
		if is.Check == biz.CheckReconcile && is.Symbol != "" {
			warnDiffs++
		}
	}
	if warnDiffs != 1 {
		t.Fatalf("want exactly the first-round diff issue, got %d of %+v", warnDiffs, issues)
	}
}

type flipDaily struct {
	name string
	bars []biz.Bar
}

func (f *flipDaily) Name() string { return f.name }

func (f *flipDaily) DailyBars(_ context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	var out []biz.Bar
	for _, b := range f.bars {
		if len(q.Symbols) == 0 || containsSymbol(q.Symbols, b.Symbol) {
			out = append(out, b)
		}
	}
	return biz.BarBatch{Bars: out, Expect: q.Expect.Format("2006-01-02")}, nil
}

func containsSymbol(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func selfHealPG(t *testing.T) *ent.Client {
	t.Helper()
	raw := os.Getenv("NT_TEST_PG_PORT")
	if raw == "" {
		t.Skip("set isolated NT_TEST_PG_PORT")
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port == 5432 || port < 1 || port > 65535 {
		t.Fatal("requires non-production isolated PG port")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := entclient.Open(ctx, entclient.Config{DB: dbinit.Config{
		Host: "127.0.0.1", Port: int32(port), User: "postgres", Password: "postgres",
		Database: "novatrader_selfheal_test", SSLMode: "disable", Timeout: 3 * time.Second,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c.Client
}
