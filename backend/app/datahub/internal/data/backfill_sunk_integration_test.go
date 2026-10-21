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

// D10 隔离 PG 集成：零行 sunk 登记 → 覆盖跳过 → RunBackfill 单次整窗 → 确认回执转净/重查。
// 与 biz 单测互补：全链路走真实 ent 仓储（equity_unlock/dataset_record/backfill_job/data_quality_issue）。

type flipUnlock struct {
	name  string
	plans []biz.UnlockPlan
	calls int
}

func (f *flipUnlock) Name() string { return f.name }
func (f *flipUnlock) UnlockPlans(_ context.Context, from, to time.Time) ([]biz.UnlockPlan, error) {
	f.calls++
	return f.plans, nil
}

type flipBlock struct {
	name string
	rows []biz.BlockTrade
}

func (f *flipBlock) Name() string { return f.name }
func (f *flipBlock) BlockTrades(context.Context, time.Time) ([]biz.BlockTrade, error) {
	return f.rows, nil
}

func TestSunkUnlockChainConfirmsAfterBackfill(t *testing.T) {
	client := sunkPG(t, "novatrader_sunk_unlock_test")
	repo := NewRepo(client)
	ctx := context.Background()
	wipeSunkTables(t, client)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())

	em := &flipUnlock{name: biz.SourceEastmoney}
	cfg := biz.Config{
		AllowUnofficial: true,
		Sources:         map[string]biz.SourceConfig{biz.SourceEastmoney: {Enabled: true, Official: true, Timeout: 30 * time.Second}},
		Domains:         map[string]biz.DomainConfig{biz.DomainUnlock: {Enabled: true, Sources: []string{biz.SourceEastmoney}}},
	}
	p, warn := biz.NewPipeline(cfg, []biz.Source{em}, nil)
	if len(warn) > 0 {
		t.Fatalf("warnings %v", warn)
	}
	col := biz.NewCollector(p, repo, nil, nil, nil, nil, biz.Options{}, log.DefaultLogger)

	// 空窗运行（真无解禁合法）→ run.Rows=0 → sunk 立即登记整窗待补。
	run, err := col.CollectUnlock(ctx, day, "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if run.Rows != 0 {
		t.Fatalf("空窗应 0 行: %d", run.Rows)
	}
	if !col.SunkEmptyBackfill(ctx, biz.DomainUnlock, day, run) {
		t.Fatal("sunk 应登记")
	}
	pending, err := repo.PendingBackfills(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending %v err=%v", pending, err)
	}
	job := pending[0]
	// date 列读回为 UTC 零点渲染，按上海日历日比价。
	sameDate := func(got, want time.Time) bool {
		gy, gm, gd := got.In(tradecal.Shanghai()).Date()
		wy, wm, wd := want.Date()
		return gy == wy && gm == wm && gd == wd
	}
	if job.Domain != biz.DomainUnlock || job.RequestedBy != "sunk:unlock" ||
		!sameDate(job.Start, day) || !sameDate(job.End, day.AddDate(0, 0, 89)) || job.Source != "" || job.Total != 90 {
		t.Fatalf("sunk job %+v", job)
	}
	// 同日重复运行不重复登记。
	if col.SunkEmptyBackfill(ctx, biz.DomainUnlock, day, run) {
		t.Fatal("同日重复运行不应再登记")
	}
	// worker 每日入口覆盖感知：当日/窗口中点均跳过采集。
	for _, probe := range []time.Time{day, day.AddDate(0, 0, 30)} {
		if ok, err := col.BackfillCovered(ctx, biz.DomainUnlock, probe); err != nil || !ok {
			t.Fatalf("covered(%v)=%v %v", probe, ok, err)
		}
	}

	// 源窗口到新解禁 → RunBackfill 单次采整窗（禁 90 日循环），落 1 行。
	em.plans = []biz.UnlockPlan{{Symbol: "000001.SZ", FreeDate: day.AddDate(0, 0, 10), LiftType: "首发原股东限售股", Shares: 120.5}}
	done := col.RunBackfill(ctx, job)
	if done.Status != biz.JobSuccess || done.Rows != 1 || done.Done != done.Total {
		t.Fatalf("backfill %+v", done)
	}
	if em.calls != 2 {
		t.Fatalf("unlock 应共 2 次调用（每日+回补各一），got %d（疑似 90 日循环）", em.calls)
	}

	// 确认回执：窗口行数>0 → 已回补确认，不登记重查，转净。
	col.ConfirmSunkBackfills(ctx)
	pending2, err := repo.PendingBackfills(ctx)
	if err != nil || len(pending2) != 0 {
		t.Fatalf("确认后应无重查任务: %v", pending2)
	}
}

func TestSunkBlockTradeRetriesUntilConfirmed(t *testing.T) {
	client := sunkPG(t, "novatrader_sunk_bt_test")
	repo := NewRepo(client)
	ctx := context.Background()
	wipeSunkTables(t, client)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())

	ts := &flipBlock{name: biz.SourceTushare}
	cfg := biz.Config{
		AllowUnofficial: true,
		Sources:         map[string]biz.SourceConfig{biz.SourceTushare: {Enabled: true, Official: true, Timeout: 30 * time.Second}},
		Domains:         map[string]biz.DomainConfig{biz.DomainBlockTrade: {Enabled: true, Sources: []string{biz.SourceTushare}}},
	}
	p, warn := biz.NewPipeline(cfg, []biz.Source{ts}, nil)
	if len(warn) > 0 {
		t.Fatalf("warnings %v", warn)
	}
	col := biz.NewCollector(p, repo, nil, nil, nil, nil, biz.Options{}, log.DefaultLogger)

	// 当日零成交（合法空页）→ sunk 登记按日待补。
	run, err := col.CollectBlockTrades(ctx, day, "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !col.SunkEmptyBackfill(ctx, biz.DomainBlockTrade, day, run) {
		t.Fatal("sunk 应登记")
	}
	pending, err := repo.PendingBackfills(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending %v err=%v", pending, err)
	}
	job := pending[0]
	sameDate := func(got, want time.Time) bool {
		gy, gm, gd := got.In(tradecal.Shanghai()).Date()
		wy, wm, wd := want.Date()
		return gy == wy && gm == wm && gd == wd
	}
	if job.RequestedBy != "sunk:block_trade" || !sameDate(job.Start, day) || !sameDate(job.End, day) {
		t.Fatalf("sunk job %+v", job)
	}

	// 源仍零行 → 任务成功完成但窗口行数=0 → 必填域确认回执登记重查（单条），附 CheckEmpty warn。
	done := col.RunBackfill(ctx, job)
	if done.Status != biz.JobSuccess || done.Rows != 0 {
		t.Fatalf("backfill %+v", done)
	}
	col.ConfirmSunkBackfills(ctx)
	pending2, err := repo.PendingBackfills(ctx)
	if err != nil || len(pending2) != 1 {
		t.Fatalf("应登记 1 条重查: %v", pending2)
	}
	if pending2[0].RequestedBy != "retry:block_trade:"+day.Format("20060102") {
		t.Fatalf("重查任务 %+v", pending2[0])
	}
	issues, err := repo.ListIssues(ctx, &day, biz.DomainBlockTrade, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 同键合并：sunk 登记 + 重查登记命中同一条 CheckEmpty warn，hits≥2。
	warnHits := 0
	for _, is := range issues {
		if is.Check == biz.CheckEmpty && is.Severity == "warn" {
			warnHits += is.Hits
		}
	}
	if warnHits < 2 {
		t.Fatalf("sunk+重查应合并命中≥2 次 warn: %+v", issues)
	}
	// 再轮询不重复登记。
	col.ConfirmSunkBackfills(ctx)
	if pending3, _ := repo.PendingBackfills(ctx); len(pending3) != 1 {
		t.Fatalf("重查应单条: %v", pending3)
	}

	// 源补出当日披露 → 重查执行 → 确认回执转净。
	ts.rows = []biz.BlockTrade{{Symbol: "600519.SH", TradeDate: day, TradePrice: 1688, Volume: 3.2, Amount: 5401.6, Buyer: "机构专用", Seller: "某营业部"}}
	retry := pending2[0]
	done2 := col.RunBackfill(ctx, retry)
	if done2.Status != biz.JobSuccess || done2.Rows != 1 {
		t.Fatalf("retry backfill %+v", done2)
	}
	col.ConfirmSunkBackfills(ctx)
	pending4, err := repo.PendingBackfills(ctx)
	if err != nil || len(pending4) != 0 {
		t.Fatalf("确认后应转净: %v", pending4)
	}
}

func sunkPG(t *testing.T, db string) *ent.Client {
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
		Database: db, SSLMode: "disable", Timeout: 3 * time.Second,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c.Client
}

// wipeSunkTables 清本链涉及的四表：共享容器上库跨次运行保留，测试各自从零起步。
func wipeSunkTables(t *testing.T, client *ent.Client) {
	t.Helper()
	ctx := context.Background()
	for _, wipe := range []func(context.Context) (int, error){
		client.BackfillJob.Delete().Exec,
		client.EquityUnlock.Delete().Exec,
		client.DatasetRecord.Delete().Exec,
		client.DataQualityIssue.Delete().Exec,
	} {
		if _, err := wipe(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
