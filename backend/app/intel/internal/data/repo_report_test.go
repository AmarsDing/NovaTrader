package data

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"server/app/intel/internal/biz"
	"server/ent"
	"server/ent/datasetrecord"
	"server/pkg/dataset"
	"server/pkg/dbinit"
	"server/pkg/entclient"

	"github.com/go-kratos/kratos/v2/log"
)

func f64p(v float64) *float64 { return &v }
func i32p(v int) *int         { return &v }

// 离线表驱动：写 dataset_record 前的清洗纪律——双 nil 年度点整条丢弃（没取到≠0），
// 全 nil 披露（无 eps/pe/aim/评级任一）判 nil，只进原文；清洗不得改动原对象。
func TestScrubForecastDiscipline(t *testing.T) {
	mixed := &biz.ReportForecast{EPS: []biz.EPSPoint{{Year: 2026}, {Year: 2027, EPS: f64p(0.7)}}}
	cases := []struct {
		name    string
		fc      *biz.ReportForecast
		wantNil bool
		wantEPS int
	}{
		{name: "nil", fc: nil, wantNil: true},
		{name: "empty struct", fc: &biz.ReportForecast{}, wantNil: true},
		{name: "all nil eps point dropped then nil", fc: &biz.ReportForecast{EPS: []biz.EPSPoint{{Year: 2026}}}, wantNil: true},
		{name: "one sided point kept", fc: &biz.ReportForecast{EPS: []biz.EPSPoint{{Year: 2026, PE: f64p(20.18)}}}, wantEPS: 1},
		{name: "mixed points filtered", fc: mixed, wantEPS: 1},
		{name: "aim only", fc: &biz.ReportForecast{AimPriceHigh: f64p(14)}, wantEPS: 0},
		{name: "aim low only", fc: &biz.ReportForecast{AimPriceLow: f64p(12.5)}, wantEPS: 0},
		{name: "rating only", fc: &biz.ReportForecast{Rating: "买入"}, wantEPS: 0},
		{name: "last rating only", fc: &biz.ReportForecast{LastRating: "增持"}, wantEPS: 0},
	}
	for _, c := range cases {
		got := scrubForecast(c.fc)
		if c.wantNil {
			if got != nil {
				t.Fatalf("%s: expected nil, got %+v", c.name, got)
			}
			continue
		}
		if got == nil || len(got.EPS) != c.wantEPS {
			t.Fatalf("%s: got %+v want eps %d", c.name, got, c.wantEPS)
		}
	}
	if len(mixed.EPS) != 2 || mixed.EPS[0].EPS != nil || mixed.EPS[0].PE != nil {
		t.Fatalf("scrub mutated the input: %+v", mixed.EPS)
	}
}

// 隔离 PG 哨兵：NT_TEST_PG_PORT 指向专用实例才跑，未设自动 skip
// （沿用 datahub dataset_test.go、strategy data_test.go 的既有写法）。
func reportForecastDB(t *testing.T) *ent.Client {
	t.Helper()
	v := os.Getenv("NT_TEST_PG_PORT")
	if v == "" {
		t.Skip("set NT_TEST_PG_PORT for isolated intel report_forecast integration")
	}
	port, err := strconv.Atoi(v)
	if err != nil || port < 1 || port > 65535 {
		t.Fatal("invalid NT_TEST_PG_PORT")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := entclient.Open(ctx, entclient.Config{DB: dbinit.Config{
		Host: "127.0.0.1", Port: int32(port), User: "postgres", Password: "postgres",
		Database: "novatrader_intel_report_test", SSLMode: "disable", Timeout: 3 * time.Second,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := conn.Client
	clean := context.Background()
	for _, del := range []func() error{
		func() error { _, err := client.DatasetRecord.Delete().Exec(clean); return err },
		func() error { _, err := client.IntelLink.Delete().Exec(clean); return err },
		func() error { _, err := client.IntelCluster.Delete().Exec(clean); return err },
		func() error { _, err := client.NewsSentiment.Delete().Exec(clean); return err },
	} {
		if err := del(); err != nil {
			t.Fatal(err)
		}
	}
	return client
}

// 含 Forecast 的 CreateItem 与 news_sentiment 同事务写 dataset_record；
// 二次同 source_id（不同内容指纹）靠 revision_hash DoNothing 不多行。
func TestReportForecastDatasetSameTransaction(t *testing.T) {
	client := reportForecastDB(t)
	ctx := context.Background()
	repo := NewRepo(client, log.DefaultLogger)
	event := time.Date(2026, 10, 19, 8, 30, 0, 0, time.UTC)
	observed := event.Add(2 * time.Hour)
	fc := &biz.ReportForecast{
		EPS: []biz.EPSPoint{
			{Year: 2026, EPS: f64p(0.64), PE: f64p(20.18)},
			{Year: 2027}, // 双 nil 年度点落库时整条丢弃——没取到≠0
			{Year: 2028, EPS: f64p(0.8)},
		},
		AimPriceLow: f64p(12.5), AimPriceHigh: f64p(14),
		Rating: "增持", LastRating: "中性", RatingChange: i32p(3), OrgName: "东吴证券",
	}
	it := &biz.Item{Source: "eastmoney", SourceID: "AP202610190001", Kind: biz.KindReport,
		Title: "贵州茅台：三季报点评", Content: "东吴证券 增持｜2026E EPS 0.64 / PE 20.18｜目标价 12.50–14.00 元",
		URL:  "https://data.eastmoney.com/report/info/AP202610190001.html",
		Hash: "fc-hash-1", Codes: []string{"600519.SH"},
		PublishTime: event, ReceivedAt: observed, Forecast: fc}
	if err := repo.CreateItem(ctx, it, biz.StatusPending, ""); err != nil {
		t.Fatal(err)
	}
	if it.ID == 0 || !it.Head {
		t.Fatalf("news row not written: %+v", it)
	}
	rows, err := client.DatasetRecord.Query().Where(datasetrecord.DomainEQ(domainReportForecast)).All(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("dataset rows = %d %v", len(rows), err)
	}
	r := rows[0]
	if r.BusinessKey != "AP202610190001" || r.Source != "eastmoney" || r.Availability != "unknown" || r.AvailableAt != nil {
		t.Fatalf("identity/availability = %+v", r)
	}
	if !r.EventTime.Equal(event) || !r.ObservedAt.Equal(observed) {
		t.Fatalf("event/observed = %v %v", r.EventTime, r.ObservedAt)
	}
	rec := dataset.Record{Domain: r.Domain, BusinessKey: r.BusinessKey, Source: r.Source, EventTime: r.EventTime,
		AvailableAt: r.AvailableAt, Availability: r.Availability, Evidence: r.AvailabilityEvidence,
		ObservedAt: r.ObservedAt, RawHash: r.RawHash, RevisionHash: r.RevisionHash, Payload: r.Payload}
	if err := rec.Verify(); err != nil {
		t.Fatalf("stored record failed integrity: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(r.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["info_code"] != "AP202610190001" || payload["rating"] != "增持" || payload["last_rating"] != "中性" {
		t.Fatalf("payload = %v", payload)
	}
	eps, ok := payload["eps"].([]any)
	if !ok || len(eps) != 2 {
		t.Fatalf("double-nil year point must be dropped: %v", payload["eps"])
	}
	if eps[0].(map[string]any)["eps"] != 0.64 || eps[0].(map[string]any)["year"] != 2026.0 {
		t.Fatalf("eps[0] = %v", eps[0])
	}

	// 同一研报另一条新闻行（不同内容指纹）：news 双写，dataset 靠 revision_hash DoNothing 幂等。
	// Codes 原样补回——生产同报重取 codes 稳定；缺它会让 payload 指纹不同、幂等断言在真库上必红（校验 must_fix）。
	again := &biz.Item{Source: "eastmoney", SourceID: "AP202610190001", Kind: biz.KindReport,
		Title: it.Title, Content: it.Content + "（重发）", Hash: "fc-hash-2",
		Codes:       it.Codes,
		PublishTime: event, ReceivedAt: observed.Add(time.Minute), Forecast: fc}
	if err := repo.CreateItem(ctx, again, biz.StatusPending, ""); err != nil {
		t.Fatal(err)
	}
	n, err := client.DatasetRecord.Query().Where(datasetrecord.BusinessKeyEQ("AP202610190001")).Count(ctx)
	if err != nil || n != 1 {
		t.Fatalf("same source_id wrote again: %d %v", n, err)
	}
}

// filtered 分支不落 dataset；无 Forecast 不落；全 nil 披露只进原文不落。
func TestReportForecastSkippedRows(t *testing.T) {
	client := reportForecastDB(t)
	ctx := context.Background()
	repo := NewRepo(client, log.DefaultLogger)
	now := time.Now()

	filtered := &biz.Item{Source: "eastmoney", SourceID: "AP-F", Kind: biz.KindReport,
		Title: "广告研报", Content: "c", Hash: "fc-f", PublishTime: now, ReceivedAt: now,
		Forecast: &biz.ReportForecast{Rating: "买入"}}
	if err := repo.CreateItem(ctx, filtered, biz.StatusFiltered, "广告"); err != nil {
		t.Fatal(err)
	}

	plain := &biz.Item{Source: "eastmoney", SourceID: "AP-P", Kind: biz.KindReport,
		Title: "无披露研报", Content: "c", Hash: "fc-p", PublishTime: now, ReceivedAt: now}
	if err := repo.CreateItem(ctx, plain, biz.StatusPending, ""); err != nil {
		t.Fatal(err)
	}

	empty := &biz.Item{Source: "eastmoney", SourceID: "AP-E", Kind: biz.KindReport,
		Title: "空披露研报", Content: "c", Hash: "fc-e", PublishTime: now, ReceivedAt: now,
		Forecast: &biz.ReportForecast{EPS: []biz.EPSPoint{{Year: 2026}}}}
	if err := repo.CreateItem(ctx, empty, biz.StatusPending, ""); err != nil {
		t.Fatal(err)
	}

	if n, err := client.DatasetRecord.Query().Count(ctx); err != nil || n != 0 {
		t.Fatalf("no disclosure must not land in dataset_record: %d %v", n, err)
	}
	if n, err := client.NewsSentiment.Query().Count(ctx); err != nil || n != 3 {
		t.Fatalf("news rows = %d %v", n, err)
	}
}
