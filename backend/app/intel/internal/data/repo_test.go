package data_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"server/app/intel/internal/biz"
	"server/app/intel/internal/data"
	"server/conf"
	"server/ent"
	"server/ent/outbox"
	"server/pkg/dbinit"
	"server/pkg/migrate"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// 集成测试用独立的 novatrader_inteltest 库，本机没有 PostgreSQL 时跳过。
func openTestDB(t *testing.T) *ent.Client {
	t.Helper()
	ctx := context.Background()
	cfg := dbinit.Config{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres",
		Database: "novatrader_inteltest", SSLMode: "disable", Timeout: 3 * time.Second,
	}
	if err := dbinit.EnsureDatabase(ctx, cfg); err != nil {
		t.Skip(err)
	}
	if err := dbinit.EnsureExtension(ctx, cfg, "vector"); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Schema(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	dsn, err := dbinit.BusinessDSN(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { client.Close() })
	for _, del := range []func() error{
		func() error { _, err := client.IntelLink.Delete().Exec(ctx); return err },
		func() error { _, err := client.IntelFact.Delete().Exec(ctx); return err },
		func() error { _, err := client.IntelCluster.Delete().Exec(ctx); return err },
		func() error { _, err := client.NewsSentiment.Delete().Exec(ctx); return err },
		func() error { _, err := client.StockAlias.Delete().Exec(ctx); return err },
		func() error { _, err := client.StockBasic.Delete().Exec(ctx); return err },
		func() error { _, err := client.Outbox.Delete().Exec(ctx); return err },
	} {
		if err := del(); err != nil {
			t.Fatal(err)
		}
	}
	return client
}

func TestRepoPipelineAgainstPostgres(t *testing.T) {
	client := openTestDB(t)
	ctx := context.Background()
	if err := client.StockBasic.Create().SetStockCode("600519.SH").SetStockName("贵州茅台").SetMarket("SH").
		SetConcept("白酒，MSCI中国").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repo := data.NewRepo(client, log.DefaultLogger)
	u := biz.NewUsecase(repo, nil, &conf.Intel{}, log.DefaultLogger)
	if err := u.Warm(ctx); err != nil {
		t.Fatal(err)
	}

	body := "贵州茅台公告称，公司因涉嫌信息披露违规，收到中国证监会立案告知书，涉及金额1.2亿元，占净资产0.5%，2026年10月8日收到。"
	head, err := u.Process(ctx, biz.Raw{Source: "cninfo", SourceID: "a1", Kind: "announcement",
		Title: "贵州茅台：关于收到立案告知书的公告", Content: body, Codes: []string{"600519.SH"}})
	if err != nil || head.Status != biz.ResultScored {
		t.Fatalf("head = %+v %v", head, err)
	}
	dup, err := u.Process(ctx, biz.Raw{Source: "eastmoney", Kind: "announcement",
		Title: "贵州茅台：关于收到立案告知书的公告", Content: body + "（来源：东方财富）"})
	if err != nil || dup.Status != biz.ResultDuplicate {
		t.Fatalf("exact copy = %+v %v", dup, err)
	}
	near, err := u.Process(ctx, biz.Raw{Source: "sina", Kind: "news",
		Title: "【快讯】贵州茅台：关于收到立案告知书的公告", Content: body})
	if err != nil || near.Status != biz.ResultNearDuplicate || near.ClusterID != head.ClusterID {
		t.Fatalf("near = %+v %v (head cluster %d)", near, err, head.ClusterID)
	}

	item, err := u.GetItem(ctx, head.NewsID)
	if err != nil {
		t.Fatal(err)
	}
	if item.EventType != biz.EventPenalty || item.ClusterSize != 2 || !item.Degraded {
		t.Fatalf("item = %+v", item)
	}
	if len(item.Links) != 1 || item.Links[0].Target != "600519.SH" || item.Links[0].Confidence != 1 {
		t.Fatalf("links = %+v", item.Links)
	}
	if len(item.Facts) != 3 {
		t.Fatalf("facts = %+v", item.Facts)
	}

	code, composite, items, err := u.Timeline(ctx, "sh600519", time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if code != "600519.SH" || len(items) != 1 || items[0].ID != head.NewsID || composite >= 0 || items[0].Content != "" {
		t.Fatalf("timeline = %s %v %+v", code, composite, items)
	}

	words, err := u.HotWords(ctx, time.Hour, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range words {
		if w.Type == biz.TargetStock && w.Target == "600519.SH" && w.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("hot words = %+v", words)
	}

	alerts, err := client.Outbox.Query().Where(outbox.Subject("intel.alert")).Count(ctx)
	if err != nil || alerts != 1 {
		t.Fatalf("alerts in outbox = %d %v", alerts, err)
	}
}

func TestRepoAlertOnceAndDuplicateHash(t *testing.T) {
	client := openTestDB(t)
	ctx := context.Background()
	repo := data.NewRepo(client, log.DefaultLogger)
	it := &biz.Item{Source: "x", Kind: biz.KindNews, Title: "t", Content: "c", Hash: "h1",
		PublishTime: time.Now(), ReceivedAt: time.Now()}
	if err := repo.CreateItem(ctx, it, biz.StatusPending, ""); err != nil {
		t.Fatal(err)
	}
	if !it.Head || it.ClusterID == 0 {
		t.Fatalf("first item = %+v", it)
	}
	again := *it
	again.ID, again.ClusterID = 0, 0
	if err := repo.CreateItem(ctx, &again, biz.StatusPending, ""); !errors.Is(err, biz.ErrDuplicate) {
		t.Fatalf("duplicate hash err = %v", err)
	}
	if n, _ := client.IntelCluster.Query().Count(ctx); n != 1 {
		t.Fatalf("duplicate left a cluster behind: %d", n)
	}

	pending, err := repo.Pending(ctx, 10)
	if err != nil || len(pending) != 1 || !pending[0].Head {
		t.Fatalf("pending = %+v %v", pending, err)
	}

	alert := mustEnvelope(t, "intel.alert")
	for i := 0; i < 2; i++ {
		ev := mustEnvelope(t, "intel.news.scored")
		if err := repo.SaveScored(ctx, &biz.Scored{
			Item:  it,
			Score: biz.Score{EventType: biz.EventPenalty, Sentiment: -0.7, Importance: 4, HalfLifeMinutes: 60, Scorer: biz.ScorerRule},
			Links: []biz.Link{{TargetType: biz.TargetStock, Target: "600519.SH", Confidence: 0.9, Method: biz.MethodName, Matched: "贵州茅台"}},
			Stock: "600519.SH",
			Event: &ev,
			Alert: &alert,
		}); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	if n, _ := client.Outbox.Query().Where(outbox.Subject("intel.alert")).Count(ctx); n != 1 {
		t.Fatalf("alert written %d times", n)
	}
	if n, _ := client.IntelLink.Query().Count(ctx); n != 1 {
		t.Fatalf("links after re-save = %d", n)
	}
}
