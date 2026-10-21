package biz

import (
	"context"
	"math"
	"testing"
	"time"

	"server/library/dataformat"
	"server/pkg/market"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

func closePtr(v float64) *float64 { return &v }

func TestCloseChgFromOverseasCloses(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d) }
	atT := func(d, h int) time.Time { return time.Date(2026, 9, 15+d, h, 0, 0, 0, time.UTC) }
	asOf := atT(0, 9)

	// 相邻两个有效收盘实算涨跌（%），PctChg 列（上游写死 0）不得参与。
	q := []market.Quote{
		{Code: "USDCNH", TradeDate: day(-2), Close: closePtr(7.10), PctChg: 0, AsOf: atT(-2, 9)},
		{Code: "USDCNH", TradeDate: day(-1), Close: closePtr(7.20), PctChg: 0, AsOf: atT(-1, 9)},
	}
	v, ok := latestCloseChg(q, asOf, "USDCNH").Value()
	if !ok || math.Abs(v-(7.20/7.10-1)*100) > 1e-9 {
		t.Fatalf("usdcnh chg = %v ok=%v, want ~1.40845", v, ok)
	}

	// 最近一日收盘缺失（NULL）→ 跨过它取最近两个有效收盘。
	q = []market.Quote{
		{Code: "USDCNH", TradeDate: day(-3), Close: closePtr(7.10), AsOf: atT(-3, 9)},
		{Code: "USDCNH", TradeDate: day(-1), Close: nil, AsOf: atT(-1, 9)},
		{Code: "USDCNH", TradeDate: day(-2), Close: closePtr(7.20), AsOf: atT(-2, 9)},
	}
	v, ok = latestCloseChg(q, asOf, "USDCNH").Value()
	if !ok || math.Abs(v-(7.20/7.10-1)*100) > 1e-9 {
		t.Fatalf("span-gap chg = %v ok=%v, want ~1.40845", v, ok)
	}

	// 同一个 trade_date 多条 → 取 AsOf 最晚一条。
	q = []market.Quote{
		{Code: "XAU", TradeDate: day(-2), Close: closePtr(2000), AsOf: atT(-2, 9)},
		{Code: "XAU", TradeDate: day(-1), Close: closePtr(2020), AsOf: atT(-1, 8)},
		{Code: "XAU", TradeDate: day(-1), Close: closePtr(2030), AsOf: atT(-1, 9)},
	}
	v, ok = latestCloseChg(q, asOf, "XAU").Value()
	if !ok || math.Abs(v-1.5) > 1e-9 {
		t.Fatalf("same-day latest-asof chg = %v ok=%v, want 1.5", v, ok)
	}

	// 样本不足两个 → 缺失（WhyEmpty），绝不编 0。
	q = []market.Quote{{Code: "USDCNH", TradeDate: day(-1), Close: closePtr(7.2), AsOf: atT(-1, 9)}}
	r := latestCloseChg(q, asOf, "USDCNH")
	if _, ok = r.Value(); ok || r.Why() != dataformat.WhyEmpty {
		t.Fatalf("single sample must be missing(empty), got %v", r)
	}

	// 无任何样本 → 缺失。
	if _, ok = latestCloseChg(nil, asOf, "USDCNH", "USDCNY").Value(); ok {
		t.Fatal("no quotes must be missing")
	}

	// 审计口径：as_of 之后才入手的行不进计算（只剩一个有效样本 → 缺失）。
	q = []market.Quote{
		{Code: "USDCNH", TradeDate: day(-2), Close: closePtr(7.10), AsOf: atT(-2, 9)},
		{Code: "USDCNH", TradeDate: day(-1), Close: closePtr(7.20), AsOf: atT(0, 20)}, // 晚于 asOf
	}
	if _, ok = latestCloseChg(q, asOf, "USDCNH").Value(); ok {
		t.Fatal("late-asof row must be excluded")
	}

	// 候选代码按序回退：USDCNH 无数据时用 USDCNY；黄金候选任一命中即可。
	q = []market.Quote{
		{Code: "USDCNY", TradeDate: day(-2), Close: closePtr(7.00), AsOf: atT(-2, 9)},
		{Code: "USDCNY", TradeDate: day(-1), Close: closePtr(6.93), AsOf: atT(-1, 9)},
		{Code: "AU9999", TradeDate: day(-2), Close: closePtr(500), AsOf: atT(-2, 9)},
		{Code: "AU9999", TradeDate: day(-1), Close: closePtr(510), AsOf: atT(-1, 9)},
	}
	v, ok = latestCloseChg(q, asOf, "USDCNH", "USDCNY").Value()
	if !ok || math.Abs(v-(-1.0)) > 1e-9 {
		t.Fatalf("USDCNY fallback chg = %v ok=%v, want -1.0", v, ok)
	}
	v, ok = latestCloseChg(q, asOf, "XAU", "XAUUSD", "GC", "AU9999", "GOLD").Value()
	if !ok || math.Abs(v-2.0) > 1e-9 {
		t.Fatalf("gold candidate chg = %v ok=%v, want 2.0", v, ok)
	}

	// Close ≤ 0 视为缺失样本。
	q = []market.Quote{
		{Code: "XAU", TradeDate: day(-2), Close: closePtr(0), AsOf: atT(-2, 9)},
		{Code: "XAU", TradeDate: day(-1), Close: closePtr(2030), AsOf: atT(-1, 9)},
	}
	if _, ok = latestCloseChg(q, asOf, "XAU").Value(); ok {
		t.Fatal("non-positive close must be dropped, one sample left → missing")
	}
}

func TestAttachExternalKeys(t *testing.T) {
	day := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	asOf := day.Add(10 * time.Hour)
	repo := &fakeRepo{
		quotes: []market.Quote{
			{Code: "USDCNH", TradeDate: day.AddDate(0, 0, -2), Close: closePtr(7.10), AsOf: day.AddDate(0, 0, -2)},
			{Code: "USDCNH", TradeDate: day.AddDate(0, 0, -1), Close: closePtr(7.20), AsOf: day.AddDate(0, 0, -1).Add(9 * time.Hour)},
			{Code: "XAU", TradeDate: day.AddDate(0, 0, -2), Close: closePtr(2000), AsOf: day.AddDate(0, 0, -2)},
			{Code: "XAU", TradeDate: day.AddDate(0, 0, -1), Close: closePtr(2030), AsOf: day.AddDate(0, 0, -1).Add(9 * time.Hour)},
		},
	}
	uc := NewUsecase(repo, &fakeSnaps{}, &fakePub{subs: map[string]int{}, last: map[string]any{}}, NewConfig(nil), log.DefaultLogger)
	uc.mu.Lock()
	uc.members = map[string][]string{"T48": {"600000.SH", "600004.SH"}}
	uc.mu.Unlock()
	heat := []market.SectorHeat{
		{SectorCode: "T48", Rank: 1, Heat: 5.4, AvgPct: 2.4, MemberCount: 2},
	}
	rows := []market.FactorRow{
		{Symbol: "600000.SH", AsOf: asOf, Values: market.Values{"pct_chg": 5}},
		{Symbol: "600004.SH", AsOf: asOf, Values: market.Values{"pct_chg": -1}},
		{Symbol: "601318.SH", AsOf: asOf, Values: market.Values{"pct_chg": 1}}, // 不在任何上榜板块
	}
	uc.attachExternal(context.Background(), day, asOf, heat, rows)

	wantUSDCNH := (7.20/7.10 - 1) * 100
	for i := range rows {
		if math.Abs(rows[i].Values[FactorUSDCNHChg]-wantUSDCNH) > 1e-9 {
			t.Fatalf("row %d usdcnh = %v, want %v", i, rows[i].Values[FactorUSDCNHChg], wantUSDCNH)
		}
		if math.Abs(rows[i].Values[FactorGoldChg]-1.5) > 1e-9 {
			t.Fatalf("row %d gold = %v, want 1.5", i, rows[i].Values[FactorGoldChg])
		}
	}
	if rows[0].Values[FactorSectorHeat] != 5.4 || rows[0].Values[FactorSectorPct] != 2.4 {
		t.Fatalf("row0 sector keys = %v/%v", rows[0].Values[FactorSectorHeat], rows[0].Values[FactorSectorPct])
	}
	if _, ok := rows[2].Values[FactorSectorHeat]; ok {
		t.Fatal("非上榜板块成分股不得有 sector.* 键（缺键=缺失）")
	}
}

// 截面任务端到端：Checkpoint 落 intraday 截面时挂上 sector.*/external.* 键；
// overseas 表无数据时外围键整体缺失（缺键），原生键照常落库。
func TestCheckpointExternalFactors(t *testing.T) {
	sh := tradecal.Shanghai()
	day := time.Date(2026, 9, 15, 0, 0, 0, 0, sh)
	if open, err := tradecal.Default.Open(day); err != nil || !open {
		t.Fatalf("%s must be a trading day", day.Format("2006-01-02"))
	}
	syms := []string{"600000.SH", "600001.SH", "600002.SH", "600003.SH", "600004.SH"}
	lasts := map[string]float64{
		"600000.SH": 10, "600001.SH": 10.5, "600002.SH": 11, "600003.SH": 9.5, "600004.SH": 10.2,
	}
	newRepo := func(quotes []market.Quote) *fakeRepo {
		stocks := make([]market.StockInfo, 0, len(syms))
		hist := map[string][]market.DayBar{}
		for _, s := range syms {
			stocks = append(stocks, market.StockInfo{Symbol: s, Name: s, FloatShare: 1e9, ListDate: day.AddDate(-10, 0, 0)})
			var bars []market.DayBar
			for i := 30; i >= 1; i-- {
				d := day.AddDate(0, 0, -i)
				bars = append(bars, market.DayBar{Symbol: s, Time: d, Open: 10, High: 10.2, Low: 9.8, Close: 10, PreClose: 10, Volume: 1e6, Amount: 1e7, Adj: 1})
			}
			hist[s] = bars
		}
		members := map[string][]string{"T48": {}}
		for _, s := range syms {
			members["T48"] = append(members["T48"], s)
		}
		return &fakeRepo{
			stocks: stocks, hist: hist, sealed: map[string]int{},
			factors: map[string][]market.FactorRow{}, boards: map[string]market.LimitBoard{},
			members: members, names: map[string]string{"T48": "测试行业"}, quotes: quotes,
		}
	}
	run := func(t *testing.T, quotes []market.Quote) map[string]market.FactorRow {
		t.Helper()
		repo := newRepo(quotes)
		snaps := &fakeSnaps{}
		uc := NewUsecase(repo, snaps, &fakePub{subs: map[string]int{}, last: map[string]any{}}, NewConfig(nil), log.DefaultLogger)
		ctx := context.Background()
		if err := uc.Warmup(ctx, day.Add(8*time.Hour+40*time.Minute)); err != nil {
			t.Fatal(err)
		}
		ts := day.Add(9*time.Hour + 30*time.Minute + 5*time.Second)
		var all []market.Snapshot
		for _, s := range syms {
			all = append(all, market.Snapshot{
				Symbol: s, Time: ts, AsOf: ts, PreClose: 10, Open: 10.2, High: lasts[s], Low: 10.1, Last: lasts[s],
				Volume: 10000, Amount: float64(10000) * lasts[s], Ask: [][2]float64{{lasts[s] + 0.01, 100}},
			})
		}
		snaps.snaps = all
		if err := uc.Refresh(ctx, ts.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		cp := day.Add(10 * time.Hour)
		if err := uc.Checkpoint(ctx, cp); err != nil {
			t.Fatal(err)
		}
		rows := repo.factors[KindIntraday]
		if len(rows) != len(syms) {
			t.Fatalf("intraday rows = %d, want %d", len(rows), len(syms))
		}
		out := map[string]market.FactorRow{}
		for _, r := range rows {
			out[r.Symbol] = r
		}
		return out
	}

	quotes := []market.Quote{
		{Code: "USDCNH", TradeDate: day.AddDate(0, 0, -2), Close: closePtr(7.10), PctChg: 0, AsOf: day.AddDate(0, 0, -1).Add(8 * time.Hour)},
		{Code: "USDCNH", TradeDate: day.AddDate(0, 0, -1), Close: closePtr(7.20), PctChg: 0, AsOf: day.AddDate(0, 0, -1).Add(9 * time.Hour)},
		{Code: "XAU", TradeDate: day.AddDate(0, 0, -2), Close: closePtr(2000), PctChg: 0, AsOf: day.AddDate(0, 0, -1).Add(8 * time.Hour)},
		{Code: "XAU", TradeDate: day.AddDate(0, 0, -1), Close: closePtr(2030), PctChg: 0, AsOf: day.AddDate(0, 0, -1).Add(9 * time.Hour)},
	}
	got := run(t, quotes)
	w := market.DefaultSectorWeights()
	var sumPct float64
	for _, s := range syms {
		sumPct += (lasts[s] - 10) / 10 * 100
	}
	avgPct := sumPct / float64(len(syms))
	wantHeat := w.Pct*avgPct + w.Limit*0 + w.Breadth*(3.0/5.0)
	wantUSDCNH := (7.20/7.10 - 1) * 100
	for _, s := range syms {
		r := got[s]
		if math.Abs(r.Values[FactorSectorPct]-avgPct) > 1e-9 {
			t.Fatalf("%s sector.pct_chg = %v, want %v", s, r.Values[FactorSectorPct], avgPct)
		}
		if math.Abs(r.Values[FactorSectorHeat]-wantHeat) > 1e-9 {
			t.Fatalf("%s sector.heat = %v, want %v", s, r.Values[FactorSectorHeat], wantHeat)
		}
		if math.Abs(r.Values[FactorUSDCNHChg]-wantUSDCNH) > 1e-9 {
			t.Fatalf("%s usdcnh_chg = %v, want %v", s, r.Values[FactorUSDCNHChg], wantUSDCNH)
		}
		if math.Abs(r.Values[FactorGoldChg]-1.5) > 1e-9 {
			t.Fatalf("%s gold_chg = %v, want 1.5", s, r.Values[FactorGoldChg])
		}
	}

	// 缺失路径：overseas 表为空 → external.* 键整体缺键，sector.* 键照常。
	got = run(t, nil)
	for _, s := range syms {
		r := got[s]
		if _, ok := r.Values[FactorUSDCNHChg]; ok {
			t.Fatalf("%s usdcnh_chg 缺数时绝不出键（禁止 0 冒充）", s)
		}
		if _, ok := r.Values[FactorGoldChg]; ok {
			t.Fatalf("%s gold_chg 缺数时绝不出键", s)
		}
		if _, ok := r.Values[FactorSectorPct]; !ok {
			t.Fatalf("%s sector.pct_chg 应照常落键", s)
		}
		if _, ok := r.Values["pct_chg"]; !ok {
			t.Fatalf("%s 原生因子键必须照常落库", s)
		}
	}
}
