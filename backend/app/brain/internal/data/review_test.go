package data

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"server/app/brain/internal/biz"
	"server/conf"
	"server/ent/dailyreview"
	"server/ent/outbox"
	"server/ent/tradesignal"
	"server/pkg/events"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

// knowledge_test 在 data 包：HTTP 客户端映射（httptest）+ review 落盘/信号的 PG 门禁测试。

// TestKnowledgeClientMapping 走真 HTTP 客户端打 httptest：校验查询字段与 pnl/degraded 映射。
func TestKnowledgeClientMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kb/cases/similar" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["top_k"] != "3" && body["topK"] != "3" && body["top_k"] != float64(3) && body["topK"] != float64(3) {
			t.Errorf("top_k = %v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"degraded": false,
			"cases": []any{
				map[string]any{"case": map[string]any{
					"signalId": "7", "stockCode": "000858.SZ", "stockName": "五粮液",
					"pattern": "首板弱转强", "outcome": "win", "attribution": "题材发酵", "pnlPct": 3.2,
				}},
				map[string]any{"case": map[string]any{
					"signalId": "9", "stockCode": "600111.SH", "pattern": "二板分歧", "outcome": "loss",
				}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	cli, cleanup, err := NewKnowledgeClient(&conf.Brain{IntelAddr: srv.URL}, log.DefaultLogger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	reply, err := cli.SimilarCases(context.Background(), biz.KbQuery{Situation: "600519.SH", StockCode: "600519.SH", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if reply == nil || reply.Degraded || len(reply.Cases) != 2 {
		t.Fatalf("reply = %+v", reply)
	}
	if reply.Cases[0].PnLPct == nil || *reply.Cases[0].PnLPct != 3.2 {
		t.Fatalf("首案 pnl 应取到: %+v", reply.Cases[0])
	}
	if reply.Cases[1].PnLPct != nil {
		t.Fatalf("pnl 缺省必须保持 nil（没取到≠0）: %+v", reply.Cases[1])
	}
}

// TestKnowledgeClientNil 未配置 intel_addr → nil 客户端，biz 标 kb_missing。
func TestKnowledgeClientNil(t *testing.T) {
	cli, cleanup, err := NewKnowledgeClient(&conf.Brain{}, log.DefaultLogger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if cli != nil {
		t.Fatal("intel_addr 为空应返回 nil 客户端")
	}
}

// TestSaveProgramReviewWithEventPG：M13-T07② 落库与 strategy.review 事件必须同一事务可见。
func TestSaveProgramReviewWithEventPG(t *testing.T) {
	client, cleanup, err := NewEntClient(&conf.Postgres{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres", Database: "novatrader", SslMode: "disable",
	}, log.DefaultLogger)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()
	day := time.Date(2026, 9, 11, 0, 0, 0, 0, tradecal.Shanghai())
	purge := func() {
		client.DailyReview.Delete().Where(dailyreview.DateEQ(day), dailyreview.KindEQ("program")).ExecX(ctx)
		client.Outbox.Delete().Where(outbox.SubjectEQ(events.SubjectStrategyReview)).ExecX(ctx)
	}
	purge()
	t.Cleanup(purge)

	repo := NewReviewRepo(client)
	if err := repo.SaveProgramReviewWithEvent(ctx, day, "程序版复盘内容一", map[string]any{"missing": []string{}}, "trace-t07", "程序版复盘 09.11"); err != nil {
		t.Fatal(err)
	}
	row := client.DailyReview.Query().Where(dailyreview.DateEQ(day), dailyreview.KindEQ("program")).OnlyX(ctx)
	if row.ReviewContent == nil || *row.ReviewContent != "程序版复盘内容一" {
		t.Fatalf("review row = %+v", row)
	}
	evs := client.Outbox.Query().Where(outbox.SubjectEQ(events.SubjectStrategyReview)).AllX(ctx)
	if len(evs) != 1 {
		t.Fatalf("outbox rows = %d, 落盘必须同事务发一条 strategy.review", len(evs))
	}
	if evs[0].TraceID != "trace-t07" || !json.Valid(evs[0].Payload) {
		t.Fatalf("outbox = %+v", evs[0])
	}
	var payload map[string]string
	_ = json.Unmarshal(evs[0].Payload, &payload)
	if payload["date"] != "2026-09-11" || payload["title"] != "程序版复盘 09.11" || payload["body"] == "" {
		t.Fatalf("payload = %v", payload)
	}
	// 同日重跑：复盘行覆盖不增行，事件再发一条（M10 按 review:date 合并窗去重）。
	if err := repo.SaveProgramReviewWithEvent(ctx, day, "程序版复盘内容二", map[string]any{}, "trace-t07b", "程序版复盘 09.11"); err != nil {
		t.Fatal(err)
	}
	if n := client.DailyReview.Query().Where(dailyreview.DateEQ(day), dailyreview.KindEQ("program")).CountX(ctx); n != 1 {
		t.Fatalf("覆盖语义破坏, review rows = %d", n)
	}
	row = client.DailyReview.Query().Where(dailyreview.DateEQ(day), dailyreview.KindEQ("program")).OnlyX(ctx)
	if row.ReviewContent == nil || *row.ReviewContent != "程序版复盘内容二" {
		t.Fatalf("重跑未覆盖: %+v", row)
	}
}

// TestSignalsOnDaysPG：M13-T08 到期终态买入信号查询（含 trade_date NULL 回退 signal_time）。
func TestSignalsOnDaysPG(t *testing.T) {
	client, cleanup, err := NewEntClient(&conf.Postgres{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres", Database: "novatrader", SslMode: "disable",
	}, log.DefaultLogger)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()
	d1 := time.Date(2026, 9, 10, 0, 0, 0, 0, tradecal.Shanghai())
	d3 := time.Date(2026, 9, 8, 0, 0, 0, 0, tradecal.Shanghai())
	d5 := time.Date(2026, 9, 4, 0, 0, 0, 0, tradecal.Shanghai())
	code := "688999.SH"
	purge := func() { client.TradeSignal.Delete().Where(tradesignal.StockCodeEQ(code)).ExecX(ctx) }
	purge()
	t.Cleanup(purge)

	mk := func(status string, td *time.Time, st time.Time) {
		c := client.TradeSignal.Create().
			SetBook("paper").SetStockCode(code).SetStockName("测试股份").
			SetSignalType(tradesignal.SignalTypeBuy).SetSignalTime(st).
			SetEntryPrice(10).SetStopLoss(9.5).SetTakeProfit(10.6).
			SetSignalReasoning("t08").SetConfidenceScore(80).SetStatus(status)
		if td != nil {
			c.SetTradeDate(*td)
		}
		c.ExecX(ctx)
	}
	mk("done", &d1, d1.Add(14*time.Hour))
	mk("closed", &d1, d1.Add(14*time.Hour))
	mk("pending", &d1, d1.Add(14*time.Hour)) // 非终态排除
	mk("done", &d3, d3.Add(10*time.Hour))    // D3 到期命中
	mk("done", nil, d1.Add(14*time.Hour))    // trade_date NULL 回退 signal_time（D1）

	repo := NewReviewRepo(client)
	rows, err := repo.SignalsOnDays(ctx, []time.Time{d1, d3, d5})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d, 期望 done+closed(D1)、done(D3)、NULL 回退(D1) 共 4 条: %+v", len(rows), rows)
	}
	var fallback *biz.SignalRow
	for i := range rows {
		if rows[i].TradeDate == nil {
			fallback = &rows[i]
		}
	}
	if fallback == nil {
		t.Fatal("trade_date NULL 的行应通过 signal_time 回退命中")
	}
}
