package biz

import (
	"context"
	"math"
	"testing"
	"time"

	"server/pkg/market"

	"github.com/go-kratos/kratos/v2/log"
)

/* p2.go 席位胜率时序（目录册增补 B2）的单元测试。 */

func TestSeatStatsCloseAggregates(t *testing.T) {
	// 样本日期全取真实交易日（2026-10-09 五 / 12 一 / 13 二 / 14 三 / 15 四 / 16 五 / 19 一 / 20 二）。
	hist := map[string][]market.DayBar{
		"A": {
			{Symbol: "A", Time: shDay(2026, 10, 9), Close: 10.0},
			{Symbol: "A", Time: shDay(2026, 10, 12), Close: 10.5},
			// 10-13 A 停牌缺收盘
			{Symbol: "A", Time: shDay(2026, 10, 14), Close: 11.0},
			{Symbol: "A", Time: shDay(2026, 10, 15), Close: 10.0},
			{Symbol: "A", Time: shDay(2026, 10, 16), Close: 9.5},
			{Symbol: "A", Time: shDay(2026, 10, 19), Close: 9.8},
			{Symbol: "A", Time: shDay(2026, 10, 20), Close: 10.5},
		},
		"B": {
			{Symbol: "B", Time: shDay(2026, 10, 9), Close: 20.0},
			{Symbol: "B", Time: shDay(2026, 10, 12), Close: 18.0},
			// 10-16 B 无收盘行
			{Symbol: "B", Time: shDay(2026, 10, 19), Close: 19.0},
			{Symbol: "B", Time: shDay(2026, 10, 20), Close: 19.5},
		},
	}
	repo := &fakeRepo{hist: hist, seatRange: []SeatRecord{
		{Symbol: "A", Day: shDay(2026, 10, 9), Seat: "Z"},
		{Symbol: "B", Day: shDay(2026, 10, 9), Seat: "Z"},
		{Symbol: "A", Day: shDay(2026, 10, 12), Seat: "Z"},
		{Symbol: "A", Day: shDay(2026, 10, 16), Seat: "Q"},
	}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	now := shDay(2026, 10, 20).Add(18 * time.Hour)
	if err := uc.SeatStatsClose(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(repo.seatSaved) != 2 {
		t.Fatalf("两席位两行，got %d", len(repo.seatSaved))
	}
	var z, q *SeatStatRow
	for i := range repo.seatSaved {
		switch repo.seatSaved[i].Seat {
		case "Z":
			z = &repo.seatSaved[i]
		case "Q":
			q = &repo.seatSaved[i]
		}
	}
	if z == nil || q == nil {
		t.Fatalf("缺席位行：%+v", repo.seatSaved)
	}
	// Z：3 笔样本，T+5 均已发生
	if z.Sample != 3 || z.SampleT5 != 3 {
		t.Fatalf("Z sample=(%d,%d) want (3,3)", z.Sample, z.SampleT5)
	}
	// Z T+1：A +0.05、B −0.1；10-12 A 的 T+1（10-13）停牌缺收盘 → r1 仅 2 项
	if z.WinRateT1 == nil || math.Abs(*z.WinRateT1-0.5) > 1e-9 ||
		z.AvgRetT1 == nil || math.Abs(*z.AvgRetT1-(-0.025)) > 1e-9 ||
		z.MedRetT1 == nil || math.Abs(*z.MedRetT1-(-0.025)) > 1e-9 {
		t.Fatalf("Z t1=(%v %v %v)", *z.WinRateT1, *z.AvgRetT1, *z.MedRetT1)
	}
	// Z T+5：A(09) −0.05；B 的 T+5（10-16）缺收盘不入值；A(12) 9.8/10.5−1≈−0.066667 → r5 仅 2 项
	want5 := (-0.05 + (9.8/10.5 - 1)) / 2
	if z.WinRateT5 == nil || *z.WinRateT5 != 0 ||
		z.AvgRetT5 == nil || math.Abs(*z.AvgRetT5-want5) > 1e-9 {
		t.Fatalf("Z t5=(%v %v)", *z.WinRateT5, *z.AvgRetT5)
	}
	// Q：10-16 上榜 A，T+1=10-19 +0.031579；T+5 未发生（越过窗口末）→ sampleT5=0 且三列 NULL（没取到≠0）
	if q.Sample != 1 || q.SampleT5 != 0 {
		t.Fatalf("Q sample=(%d,%d) want (1,0)", q.Sample, q.SampleT5)
	}
	if q.WinRateT1 == nil || *q.WinRateT1 != 1 ||
		q.AvgRetT1 == nil || math.Abs(*q.AvgRetT1-(9.8/9.5-1)) > 1e-9 {
		t.Fatalf("Q t1=(%v %v)", *q.WinRateT1, *q.AvgRetT1)
	}
	if q.WinRateT5 != nil || q.AvgRetT5 != nil || q.MedRetT5 != nil {
		t.Fatalf("Q t5 未发生必须保持 NULL：%+v", q)
	}
}

func TestSeatStatsCloseAllPriceMissingKeepsNil(t *testing.T) {
	// 标的窗口无任何日线：样本数是事实照常落，收益率/胜率六列保持 NULL。
	repo := &fakeRepo{seatRange: []SeatRecord{
		{Symbol: "X", Day: shDay(2026, 10, 9), Seat: "Z"},
		{Symbol: "X", Day: shDay(2026, 10, 13), Seat: "Z"},
	}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	if err := uc.SeatStatsClose(context.Background(), shDay(2026, 10, 20).Add(18*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(repo.seatSaved) != 1 {
		t.Fatalf("仍落一行（样本数是事实），got %d", len(repo.seatSaved))
	}
	row := repo.seatSaved[0]
	if row.Sample != 2 || row.SampleT5 != 2 {
		t.Fatalf("sample=(%d,%d) want (2,2)", row.Sample, row.SampleT5)
	}
	if row.WinRateT1 != nil || row.AvgRetT1 != nil || row.MedRetT1 != nil ||
		row.WinRateT5 != nil || row.AvgRetT5 != nil || row.MedRetT5 != nil {
		t.Fatalf("有效样本为零六列必须 NULL：%+v", row)
	}
}

func TestSeatStatsCloseEmptyNoRows(t *testing.T) {
	// 窗内零龙虎榜（合法日子）不落行，也不调用仓储。
	repo := &fakeRepo{}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	if err := uc.SeatStatsClose(context.Background(), shDay(2026, 10, 20).Add(18*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(repo.seatSaved) != 0 {
		t.Fatalf("不该写库：%+v", repo.seatSaved)
	}
}

func TestSeatStatsSnapshotOrderAndTop(t *testing.T) {
	w := 0.8
	repo := &fakeRepo{seatRows: []SeatStatRow{
		{Seat: "LOW", Sample: 1, SampleT5: 0, WinRateT1: &w, Source: SourceMarketAgg},
		{Seat: "HIGH", Sample: 9, SampleT5: 7, WinRateT1: &w, Source: SourceMarketAgg},
		{Seat: "MID", Sample: 5, SampleT5: 5, WinRateT1: &w, Source: SourceMarketAgg},
	}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	day := shDay(2026, 10, 20)
	report, err := uc.SeatStatsSnapshot(context.Background(), day, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Found || len(report.Rows) != 3 {
		t.Fatalf("found=%v rows=%d", report.Found, len(report.Rows))
	}
	if report.Rows[0].Seat != "HIGH" || report.Rows[1].Seat != "MID" || report.Rows[2].Seat != "LOW" {
		t.Fatalf("应按 Sample 倒序：%+v", report.Rows)
	}
	cut, err := uc.SeatStatsSnapshot(context.Background(), day, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(cut.Rows) != 2 || cut.Rows[1].Seat != "MID" {
		t.Fatalf("top=2 截断：%+v", cut.Rows)
	}
}
