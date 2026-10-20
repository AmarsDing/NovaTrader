package data

import (
	"context"
	"math"
	"testing"
	"time"

	"server/app/market/internal/biz"
	"server/pkg/tradecal"
)

func fp(v float64) *float64 { return &v }

// 席位胜率（B2）：SaveSeatStats upsert 幂等 + 样本为零的期保持 NULL（没取到≠0）。
func TestSaveSeatStatsUpsertNullRoundtrip(t *testing.T) {
	c := openTestDB(t)
	ctx := context.Background()
	if _, err := c.SeatStats.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	r := NewRepo(c)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
	rows := []biz.SeatStatRow{
		{Seat: "Z", AsOf: day, Lookback: 60, Sample: 3, SampleT5: 3,
			WinRateT1: fp(0.5), AvgRetT1: fp(-0.025), MedRetT1: fp(-0.025),
			WinRateT5: fp(0), AvgRetT5: fp(-0.058333), MedRetT5: fp(-0.058333),
			Source: "market_agg"},
		{Seat: "Q", AsOf: day, Lookback: 60, Sample: 1, SampleT5: 0,
			WinRateT1: fp(1), AvgRetT1: fp(0.031579), MedRetT1: fp(0.031579),
			Source: "market_agg"},
		// 两期全部有效样本=0：六列保持 NULL
		{Seat: "W", AsOf: day, Lookback: 60, Sample: 2, SampleT5: 2, Source: "market_agg"},
	}
	if err := r.SaveSeatStats(ctx, rows); err != nil {
		t.Fatal(err)
	}
	// 幂等重跑：冲突列覆盖新值
	if err := r.SaveSeatStats(ctx, []biz.SeatStatRow{
		{Seat: "Z", AsOf: day, Lookback: 60, Sample: 4, SampleT5: 4,
			WinRateT1: fp(0.6), Source: "market_agg"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := r.SeatStatsRows(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("count=%d want 3（同键二次写不重复落行）", len(got))
	}
	var z, q, w *biz.SeatStatRow
	for i := range got {
		switch got[i].Seat {
		case "Z":
			z = &got[i]
		case "Q":
			q = &got[i]
		case "W":
			w = &got[i]
		}
	}
	// Z：二次覆盖生效（WinRateT1=0.6/Sample=4），未提供的列按 UpdateNewValues 被新行值覆盖为 NULL
	if z == nil || z.Sample != 4 || z.WinRateT1 == nil || math.Abs(*z.WinRateT1-0.6) > 1e-9 {
		t.Fatalf("Z=%+v", z)
	}
	// Q：T+5 未发生的三列保持 NULL
	if q == nil || q.SampleT5 != 0 || q.WinRateT5 != nil || q.AvgRetT5 != nil || q.MedRetT5 != nil {
		t.Fatalf("Q=%+v", q)
	}
	// W：全部有效样本为零，六列往返为 NULL（不落 0）
	if w == nil || w.Sample != 2 || w.SampleT5 != 2 ||
		w.WinRateT1 != nil || w.AvgRetT1 != nil || w.MedRetT1 != nil ||
		w.WinRateT5 != nil || w.AvgRetT5 != nil || w.MedRetT5 != nil {
		t.Fatalf("W=%+v", w)
	}
}
