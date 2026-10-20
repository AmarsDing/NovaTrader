package data

import (
	"context"
	"testing"
	"time"

	"server/app/datahub/internal/biz"
	"server/ent/marginmarket"
)

// B1：UpsertMarginMkt 幂等 + Nillable 缺列 NULL 往返（不降 0）。
func TestUpsertMarginMktIdempotentNullRoundtrip(t *testing.T) {
	client := captureClient(t)
	repo := NewRepo(client)
	ctx := context.Background()
	day := time.Date(2026, 10, 17, 0, 0, 0, 0, shanghaiLoc())
	rd := biz.Ready{Domain: "", TradeDate: "2026-10-17", Source: biz.SourceTushare}
	// Domain 置空避开 outbox 依赖；写库语义独立断言。
	f1, f2, f9 := 1.0, 2.0, 9.0
	var i1 int64 = 100
	items := []biz.MarginMkt{
		{Exchange: "SSE", Rzye: &f1, Rzmre: &f2},
		{Exchange: "SZSE", Rqmcl: &i1},
		{Exchange: "BSE"},
	}
	if n, err := repo.UpsertMarginMkt(ctx, day, items, "tushare", rd); err != nil || n != 3 {
		t.Fatalf("first upsert n=%d err=%v", n, err)
	}
	// 同键再来一批：SSE.Rzye 更新为 9，SZSE/BSE 不重复。
	items2 := []biz.MarginMkt{{Exchange: "SSE", Rzye: &f9}}
	if n, err := repo.UpsertMarginMkt(ctx, day, items2, "tushare", rd); err != nil || n != 1 {
		t.Fatalf("second upsert n=%d err=%v", n, err)
	}
	cnt, err := client.MarginMarket.Query().Where(marginmarket.TradeDateEQ(day)).Count(ctx)
	if err != nil || cnt != 3 {
		t.Fatalf("count=%d err=%v（幂等键 trade_date+exchange 不应重复落行）", cnt, err)
	}
	sse, err := client.MarginMarket.Query().Where(marginmarket.TradeDateEQ(day), marginmarket.ExchangeEQ("SSE")).Only(ctx)
	if err != nil || sse.Rzye == nil || *sse.Rzye != 9.0 || sse.Rzmre == nil || *sse.Rzmre != 2.0 {
		t.Fatalf("SSE=%+v err=%v（upsert 应覆盖冲突列、保留其余）", sse, err)
	}
	bse, err := client.MarginMarket.Query().Where(marginmarket.TradeDateEQ(day), marginmarket.ExchangeEQ("BSE")).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bse.Rzye != nil || bse.Rqmcl != nil || bse.Rzrqye != nil {
		t.Fatalf("BSE 全缺列必须保持 NULL：%+v", bse)
	}
	szse, err := client.MarginMarket.Query().Where(marginmarket.TradeDateEQ(day), marginmarket.ExchangeEQ("SZSE")).Only(ctx)
	if err != nil || szse.Rqmcl == nil || *szse.Rqmcl != 100 || szse.Rzye != nil {
		t.Fatalf("SZSE=%+v err=%v（写入列补齐、缺列 NULL）", szse, err)
	}
}
