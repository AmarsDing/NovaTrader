package kbstore_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"server/app/intel/internal/biz/kb"
	"server/app/intel/internal/data/kbstore"
	"server/conf"
	"server/ent"
	"server/ent/kbcase"
	"server/ent/kbdocument"
	"server/pkg/dbinit"
	"server/pkg/migrate"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// 集成测试用独立的 novatrader_kbtest 库，本机没有 PostgreSQL 时跳过。
func openTestDB(t *testing.T) *ent.Client {
	t.Helper()
	ctx := context.Background()
	cfg := dbinit.Config{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres",
		Database: "novatrader_kbtest", SSLMode: "disable", Timeout: 3 * time.Second,
	}
	if err := dbinit.EnsureDatabase(ctx, cfg); err != nil {
		t.Skip(err)
	}
	if err := dbinit.EnsureExtension(ctx, cfg, "vector"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrate.Schema(ctx, cfg); err != nil {
			t.Fatalf("migrate run %d: %v", i+1, err)
		}
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
	if _, err := client.KbCase.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.KbDocument.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return client
}

func newUsecase(t *testing.T, client *ent.Client, provider string) *kb.KnowledgeUsecase {
	t.Helper()
	c := &conf.Kb{EmbedProvider: provider}
	emb, err := kbstore.NewEmbedder(c)
	if err != nil {
		t.Fatal(err)
	}
	return kb.NewKnowledgeUsecase(kbstore.NewKnowledgeRepo(client), emb, c, log.DefaultLogger)
}

const playbook = `# 超短线打法手册

## 弱转强

首板次日低开，竞价量能放大，开盘后快速翻红，视为弱转强。买点在翻红确认后，止损设在开盘价下方一个 ATR。

## 强势回踩

连板股第一次回踩五日线且缩量，次日放量反包时介入。贵州茅台这类大盘股不适用。
`

func ingest(t *testing.T, uc *kb.KnowledgeUsecase, source, body string) *kb.IngestResult {
	t.Helper()
	res, err := uc.Ingest(context.Background(), kb.IngestInput{
		Category: "rule", Source: source, SourceType: "file",
		FileName: "playbook.md", Content: []byte(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestIngestSearchAndVersions(t *testing.T) {
	client := openTestDB(t)
	uc := newUsecase(t, client, "hash")
	ctx := context.Background()

	first := ingest(t, uc, `D:\kb\playbook.md`, playbook)
	if first.Duplicate || !first.Embedded || first.Document.ChunkCount == 0 {
		t.Fatalf("first ingest = %+v", first)
	}
	again := ingest(t, uc, `D:\kb\playbook.md`, playbook)
	if !again.Duplicate || again.Document.ID != first.Document.ID {
		t.Fatalf("duplicate ingest = %+v", again)
	}

	res, err := uc.Search(ctx, kb.SearchInput{Query: "弱转强的买点"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Degraded || len(res.Hits) == 0 {
		t.Fatalf("search = %+v", res)
	}
	top := res.Hits[0]
	if top.DocID != first.Document.ID || top.ChunkID == 0 || top.Title != "playbook" ||
		top.Source != `D:\kb\playbook.md` || !strings.Contains(top.Heading, "弱转强") {
		t.Fatalf("top hit = %+v", top)
	}
	if top.KeywordRank != 1 || top.VectorRank == 0 {
		t.Fatalf("ranks = %d / %d", top.KeywordRank, top.VectorRank)
	}

	v2 := ingest(t, uc, `D:\kb\playbook.md`, playbook+"\n## 竞价抢筹\n\n集合竞价阶段成交额排名前列且高开不超过百分之五。\n")
	if v2.Document.Version != 2 {
		t.Fatalf("version = %d", v2.Document.Version)
	}
	old, err := client.KbDocument.Get(ctx, first.Document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != kbdocument.StatusSuperseded || old.SupersededAt == nil {
		t.Fatalf("old version status = %s", old.Status)
	}
	res, err = uc.Search(ctx, kb.SearchInput{Query: "弱转强", TopK: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Hits {
		if h.DocID == first.Document.ID {
			t.Fatal("superseded version must not be searchable")
		}
	}

	res, err = uc.Search(ctx, kb.SearchInput{Query: "竞价", Categories: []string{"compliance"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("category filter leaked %d hits", len(res.Hits))
	}

	n, err := kbstore.NewKnowledgeRepo(client).Purge(ctx, time.Now().Add(40*24*time.Hour), time.Now().Add(10*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d docs, want 1", n)
	}
	if _, err := client.KbDocument.Get(ctx, v2.Document.ID); err != nil {
		t.Fatalf("active version must survive purge: %v", err)
	}
}

func TestRejectsUnknownSource(t *testing.T) {
	client := openTestDB(t)
	uc := newUsecase(t, client, "")
	for _, in := range []kb.IngestInput{
		{Category: "rule", Source: " ", SourceType: "file", FileName: "a.md", Content: []byte("内容")},
		{Category: "rule", Source: "x", SourceType: "rumor", FileName: "a.md", Content: []byte("内容")},
		{Category: "rule", Source: "ftp://a", SourceType: "url", FileName: "a.md", Content: []byte("内容")},
		{Category: "rule", Source: "群聊", SourceType: "manual", FileName: "a.md", Content: []byte("内容")},
		{Category: "rule", Source: "signal:paper:1", SourceType: "signal", FileName: "a.md", Content: []byte("内容")},
		{Category: "gossip", Source: "x", SourceType: "file", FileName: "a.md", Content: []byte("内容")},
		{Category: "rule", Source: "x", SourceType: "file", FileName: "a.doc", Content: []byte("内容")},
	} {
		if _, err := uc.Ingest(context.Background(), in); err == nil {
			t.Fatalf("expected rejection for %+v", in)
		}
	}
}

func TestKeywordOnlyThenBackfill(t *testing.T) {
	client := openTestDB(t)
	ctx := context.Background()
	plain := newUsecase(t, client, "none")
	res := ingest(t, plain, "https://example.com/rules", playbook)
	if res.Embedded || res.Document.EmbedStatus != "pending" {
		t.Fatalf("ingest without embedder = %+v", res)
	}
	sr, err := plain.Search(ctx, kb.SearchInput{Query: "强势回踩"})
	if err != nil {
		t.Fatal(err)
	}
	if !sr.Degraded || len(sr.Hits) == 0 || sr.Hits[0].VectorRank != 0 {
		t.Fatalf("keyword-only search = %+v", sr)
	}

	hashed := newUsecase(t, client, "hash")
	n, err := hashed.EmbedPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != res.Document.ChunkCount {
		t.Fatalf("backfilled %d chunks, want %d", n, res.Document.ChunkCount)
	}
	doc, err := client.KbDocument.Get(ctx, res.Document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.EmbedStatus != kbdocument.EmbedStatusDone {
		t.Fatalf("embed_status = %s", doc.EmbedStatus)
	}
	if n, _ := hashed.EmbedPending(ctx); n != 0 {
		t.Fatalf("second backfill embedded %d chunks", n)
	}
}

func TestSignalCases(t *testing.T) {
	client := openTestDB(t)
	uc := newUsecase(t, client, "hash")
	ctx := context.Background()
	sh := time.FixedZone("CST", 8*3600)
	pnl := 3.2
	sig := kb.SignalCase{
		SignalID: 1203, Book: "paper", Status: "closed",
		StockCode: "600519.SH", StockName: "贵州茅台", Strategy: "first_board_weak2strong",
		SignalType: "buy", Pattern: "弱转强", EmotionPhase: "发酵",
		SignalTime: time.Date(2026, 10, 8, 9, 41, 0, 0, sh),
		ClosedAt:   time.Date(2026, 10, 10, 14, 50, 0, 0, sh),
		PnlPct:     &pnl, Attribution: "", Reasoning: "首板次日竞价放量，翻红确认。",
	}
	first, err := uc.RecordCase(ctx, sig)
	if err != nil {
		t.Fatal(err)
	}
	second, err := uc.RecordCase(ctx, sig)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || second.CaseID != first.CaseID {
		t.Fatalf("record twice = %+v / %+v", first, second)
	}
	if n := client.KbCase.Query().Where(kbcase.SignalIDEQ(1203)).CountX(ctx); n != 1 {
		t.Fatalf("kb_case rows = %d", n)
	}

	loss := -2.5
	other := sig
	other.SignalID, other.Pattern, other.PnlPct, other.Attribution = 1204, "强势回踩", &loss, "emotion_mismatch"
	other.ClosedAt = other.ClosedAt.Add(24 * time.Hour)
	if _, err := uc.RecordCase(ctx, other); err != nil {
		t.Fatal(err)
	}
	expired := sig
	expired.SignalID, expired.Status, expired.PnlPct = 1205, "expired", nil
	if _, err := uc.RecordCase(ctx, expired); err != nil {
		t.Fatal(err)
	}
	twin := expired
	twin.SignalID = 1206
	if res, err := uc.RecordCase(ctx, twin); err != nil || !res.Created {
		t.Fatalf("signal with identical fields = %+v, %v", res, err)
	}
	row := client.KbCase.Query().Where(kbcase.SignalIDEQ(1205)).OnlyX(ctx)
	if row.Outcome != kbcase.OutcomeUnfilled {
		t.Fatalf("expired outcome = %s", row.Outcome)
	}

	before, err := uc.SimilarCases(ctx, kb.SimilarInput{Situation: "首板弱转强", AsOf: sig.ClosedAt})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Cases) != 0 {
		t.Fatalf("as_of must hide cases closed later, got %d", len(before.Cases))
	}
	after, err := uc.SimilarCases(ctx, kb.SimilarInput{Situation: "首板弱转强翻红", AsOf: sig.ClosedAt.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Cases) == 0 || after.Cases[0].Case.SignalID == 1204 {
		t.Fatalf("similar cases = %+v", after.Cases)
	}
	if after.Cases[0].ChunkID == 0 || after.Cases[0].Case.DocID == nil {
		t.Fatalf("case hit lacks citation: %+v", after.Cases[0])
	}
	yearEnd := time.Date(2026, 12, 31, 0, 0, 0, 0, sh)
	filtered, err := uc.SimilarCases(ctx, kb.SimilarInput{Situation: "回踩", Pattern: "强势回踩", AsOf: yearEnd})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Cases) != 1 || filtered.Cases[0].Case.Attribution != "emotion_mismatch" {
		t.Fatalf("pattern filter = %+v", filtered.Cases)
	}
	latest, err := uc.SimilarCases(ctx, kb.SimilarInput{AsOf: yearEnd, TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Cases) != 2 || latest.Cases[0].Case.SignalID != 1204 {
		t.Fatalf("latest cases = %+v", latest.Cases)
	}

	if _, err := uc.RecordCase(ctx, kb.SignalCase{SignalID: 9, Book: "paper", Status: "pending", StockCode: "000001.SZ", SignalTime: time.Now()}); err == nil {
		t.Fatal("non-terminal status must be rejected")
	}
}
