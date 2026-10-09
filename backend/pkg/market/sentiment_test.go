package market

import (
	"math"
	"testing"
	"time"
)

func TestComputeSentiment(t *testing.T) {
	stocks := []StockDay{
		{Symbol: "a", PctChg: 10, Amount: 100, UpStatus: StatusSealed, Consecutive: 3, PrevSealed: true},
		{Symbol: "b", PctChg: 10, Amount: 100, UpStatus: StatusSealed, Consecutive: 1, PrevSealed: true, OneWord: true},
		{Symbol: "c", PctChg: 4, Amount: 100, UpStatus: StatusBroken, PrevSealed: true},
		{Symbol: "d", PctChg: -10, Amount: 100, DownStatus: StatusSealed},
		{Symbol: "e", PctChg: 0, Amount: 100},
		{Symbol: "f", PctChg: 44, Amount: 100, NoLimit: true, UpStatus: StatusSealed},
		{Symbol: "g", Suspended: true, PctChg: 0},
	}
	s := ComputeSentiment(stocks)
	if s.UpCount != 2 || s.BrokenCount != 1 || s.DownCount != 1 || s.MaxHeight != 3 {
		t.Fatalf("%+v", s)
	}
	if math.Abs(s.BrokenRate-1.0/3) > 1e-12 {
		t.Fatal(s.BrokenRate)
	}
	// 赚钱效应剔除一字板 b：(10 + 4) / 2。
	if s.ProfitEffect != 7 {
		t.Fatal(s.ProfitEffect)
	}
	if s.Advance != 4 || s.Decline != 1 || s.Flat != 1 || s.Amount != 600 {
		t.Fatalf("%+v", s)
	}
}

func TestClassifyPhases(t *testing.T) {
	r := DefaultRules()
	cases := []struct {
		s    Sentiment
		prev Phase
		want Phase
	}{
		{Sentiment{UpCount: 20, DownCount: 40}, PhaseRecover, PhaseIce},
		{Sentiment{UpCount: 60, ProfitEffect: -4}, PhaseWarm, PhaseIce},
		{Sentiment{UpCount: 60, BrokenRate: 0.45, ProfitEffect: 1}, PhaseWarm, PhaseFade},
		{Sentiment{UpCount: 60, BrokenRate: 0.2, ProfitEffect: -1}, PhaseHot, PhaseFade},
		{Sentiment{UpCount: 90, BrokenRate: 0.2, MaxHeight: 6, ProfitEffect: 4}, PhaseWarm, PhaseHot},
		{Sentiment{UpCount: 60, BrokenRate: 0.3, MaxHeight: 3, ProfitEffect: 1}, PhaseRecover, PhaseWarm},
		{Sentiment{UpCount: 40, BrokenRate: 0.3, ProfitEffect: 1}, PhaseIce, PhaseRecover},
	}
	for i, c := range cases {
		got := r.Classify(c.s, c.prev)
		if got.Phase != c.want {
			t.Fatalf("case %d: %s want %s", i, got.Phase, c.want)
		}
		if got.ScoreCoef == 0 || got.PositionScale == 0 {
			t.Fatalf("case %d: coefficients missing", i)
		}
	}
}

func TestParseRulesKeepsDefaults(t *testing.T) {
	r, err := ParseRules(`{"hot_min_up":100,"phases":{"HOT":{"score_coef":1.1,"position_scale":0.9}}}`)
	if err != nil || r.HotMinUp != 100 || r.IceMaxUp != 30 || r.Phases[PhaseHot].ScoreCoef != 1.1 || r.Phases[PhaseIce].PositionScale != 0.3 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := ParseRules(`{"phases":null}`); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRules(`{bad`); err == nil {
		t.Fatal("bad json")
	}
}

func TestRankSectorsAndLeader(t *testing.T) {
	early := at("09:35:00")
	late := at("10:35:00")
	day := map[string]MemberDay{
		"a": {Symbol: "a", PctChg: 10, Amount: 2e8, UpStatus: StatusSealed, Consecutive: 2, FirstSealAt: late},
		"b": {Symbol: "b", PctChg: 10, Amount: 2e8, UpStatus: StatusSealed, Consecutive: 2, FirstSealAt: early},
		"c": {Symbol: "c", PctChg: 3, Amount: 2e8},
		"d": {Symbol: "d", PctChg: 1, Amount: 2e8},
		"e": {Symbol: "e", PctChg: -1, Amount: 2e8},
		"f": {Symbol: "f", PctChg: 2, Amount: 2e8},
		"g": {Symbol: "g", PctChg: 1, Amount: 2e8},
		"h": {Symbol: "h", PctChg: 5, Amount: 5e7},
	}
	members := map[string][]string{
		"AI":    {"a", "b", "c", "d", "e"},
		"BANK":  {"c", "d", "e", "f", "g", "h"},
		"SMALL": {"a", "b"},
	}
	out := RankSectors(members, day, map[string]float64{"BANK": 2}, DefaultSectorWeights())
	if len(out) != 2 || out[0].SectorCode != "AI" || out[0].Rank != 1 || out[0].LeaderSymbol != "b" {
		t.Fatalf("%+v", out)
	}
	// BANK 没有涨停股，成交额不足 1 亿的 h 不能当龙头。
	if out[1].LeaderSymbol != "c" || out[1].OverseasImpulse != 2 {
		t.Fatalf("%+v", out[1])
	}
}

func TestImpulseUsesOnlyFreshQuotes(t *testing.T) {
	sh := at("00:00:00").Location()
	d := func(s string) time.Time { x, _ := time.ParseInLocation("2006-01-02 15:04", s, sh); return x }
	quotes := []Quote{
		{Code: "NVDA", TradeDate: d("2026-10-07 00:00"), PctChg: 9, AsOf: d("2026-10-08 05:00")},
		{Code: "NVDA", TradeDate: d("2026-10-08 00:00"), PctChg: 3, AsOf: d("2026-10-09 05:00")},
		{Code: "NVDA", TradeDate: d("2026-10-09 00:00"), PctChg: -5, AsOf: d("2026-10-10 05:00")},
		{Code: "XAU", TradeDate: d("2026-10-06 00:00"), PctChg: 2, AsOf: d("2026-10-07 05:00")},
	}
	maps := []Mapping{{Asset: "NVDA", Sector: "AI", Weight: 0.5}, {Asset: "XAU", Sector: "GOLD", Weight: 1}}
	// 10-09 早盘：A 股上一交易日 10-08，取美股 10-08 的收盘。
	got := Impulse(quotes, maps, d("2026-10-08 00:00"), d("2026-10-09 09:00"))
	if got["AI"] != 1.5 || got["GOLD"] != 0 {
		t.Fatalf("%v", got)
	}
}

func TestCapitalAndAuctionFactors(t *testing.T) {
	f := FlowFactors(1e8, []Flow{{MainNet: 1e6}, {MainNet: -2e6}, {MainNet: 5e6}})
	if f["main_net_ratio"] != 0.05 || f["main_net_5d"] != 4e6 {
		t.Fatalf("%v", f)
	}
	l := LhbFactors([]Seat{
		{Name: "机构专用", Buy: 3e7, Sell: 1e7},
		{Name: "某游资", Buy: 2e7},
		{Name: "无标签", Buy: 1e6, Sell: 2e6},
	}, map[string]string{"机构专用": SeatInstitution, "某游资": SeatHotMoney})
	if l["lhb_net"] != 39e6 || l["lhb_inst_net"] != 2e7 || l["lhb_hot_buyers"] != 1 {
		t.Fatalf("%v", l)
	}
	pre := Snapshot{Bid: [][2]float64{{11, 5000}}, Ask: [][2]float64{{11, 1000}}}
	a := AuctionFactors(AuctionInput{
		Open:      Snapshot{PreClose: 10, Open: 11, Volume: 2400, Amount: 26400},
		PreMatch:  &pre,
		MA5Volume: 240000, FloatShare: 240000, PrevAmount: 264000, UpLimit: 11,
	})
	if math.Abs(a["auction_pct"]-10) > 1e-9 || a["auction_vol_ratio"] != 2.4 || a["auction_turnover"] != 1 ||
		a["auction_amount_prev"] != 10 || a["auction_unmatched"] != 4000 || a["auction_limit_up"] != 1 {
		t.Fatalf("%v", a)
	}
}
