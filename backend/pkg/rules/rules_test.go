package rules

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

var (
	ctx   = context.Background()
	asOf  = time.Date(2026, 10, 9, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	since = asOf.AddDate(-2, 0, 0)
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %.4f, want %.4f", name, got, want)
	}
}

func flat(n int, close float64) []Bar {
	bars := make([]Bar, n)
	for i := range bars {
		bars[i] = Bar{Open: close, High: close + 0.1, Low: close - 0.1, Close: close, Volume: 1e7, Amount: 1e8}
	}
	return bars
}

// firstBoardSnap：30 根 10 元附近的平盘，昨日放量封板到 11.00，今日高开 2% 走到 11.45。
func firstBoardSnap() Snapshot {
	bars := flat(29, 10)
	bars = append(bars, Bar{Open: 10.2, High: 11, Low: 10.1, Close: 11, Volume: 2.3e7, Amount: 2.5e8})
	return Snapshot{
		Symbol: "600000.SH", Name: "浦发银行", ListDate: since, AsOf: asOf, Stage: StageWarm,
		Bars:  bars,
		Today: &Bar{Open: 11.22, High: 11.5, Low: 11.2, Close: 11.45, Volume: 5e6, Amount: 5.7e7},
	}
}

// pullbackSnap：前 20 日从 10 涨到 11.9，第 21 日涨停，第 22 日见高点 14.3，之后缩量回落到 MA10 附近。
func pullbackSnap() Snapshot {
	var bars []Bar
	for i := 0; i < 20; i++ {
		c := 10 + 0.1*float64(i)
		bars = append(bars, Bar{Open: c - 0.05, High: c + 0.1, Low: c - 0.1, Close: c, Volume: 1e7, Amount: c * 1e7})
	}
	bars = append(bars,
		Bar{Open: 12, High: 13.09, Low: 11.95, Close: 13.09, Volume: 2e7, Amount: 2.6e8},
		Bar{Open: 13.2, High: 14.3, Low: 13.1, Close: 14.0, Volume: 2e7, Amount: 2.8e8},
	)
	for _, c := range []float64{13.8, 13.5, 13.3, 13.1, 12.9, 12.8, 12.7} {
		bars = append(bars, Bar{Open: c + 0.1, High: c + 0.2, Low: c - 0.1, Close: c, Volume: 1e7, Amount: c * 1e7})
	}
	bars = append(bars, Bar{Open: 12.7, High: 12.75, Low: 12.55, Close: 12.6, Volume: 5e6, Amount: 6.3e7})
	return Snapshot{
		Symbol: "000001.SZ", Name: "平安银行", ListDate: since, AsOf: asOf, Stage: StageWarm,
		Bars:  bars,
		Today: &Bar{Open: 12.6, High: 12.9, Low: 12.6, Close: 12.85, Volume: 3e6, Amount: 3.8e7},
	}
}

func TestIndicators(t *testing.T) {
	bars := flat(20, 10)
	ma, ok := MA(bars, 5)
	if !ok || ma != 10 {
		t.Fatalf("ma=%v ok=%v", ma, ok)
	}
	atr, ok := ATR(bars, 14)
	if !ok {
		t.Fatal("atr not ok")
	}
	near(t, "atr", atr, 0.2, 1e-9)
	if _, ok := ATR(bars[:14], 14); ok {
		t.Fatal("ATR needs n+1 bars")
	}
}

func TestFirstBoardHit(t *testing.T) {
	fb := NewFirstBoard(DefaultFirstBoard(), DefaultPrice())
	s := firstBoardSnap()
	if !fb.Filter(ctx, s) {
		t.Fatal("filter should pass")
	}
	// 40 + 封板 15 + 放量 2.5/3×15 + 高开 2% 15 + 高走 (11.45-11.22)/11/0.05×10
	want := 40 + 15 + 2.5/3*15 + 15 + (11.45-11.22)/11/0.05*10
	near(t, "score", fb.Score(ctx, s), want, 1e-6)
	plan, ok := fb.EntryPlan(ctx, s)
	if !ok || plan.Price != 11.45 || plan.HoldDays != 2 || plan.Side != SideBuy {
		t.Fatalf("plan=%+v ok=%v", plan, ok)
	}
}

func TestFirstBoardRejects(t *testing.T) {
	fb := NewFirstBoard(DefaultFirstBoard(), DefaultPrice())

	s := firstBoardSnap()
	s.Bars[26] = Bar{Open: 10, High: 11, Low: 10, Close: 11, Volume: 1e7, Amount: 1e8}
	s.Bars[27] = Bar{Open: 11, High: 11.1, Low: 10, Close: 10, Volume: 1e7, Amount: 1e8}
	if fb.Filter(ctx, s) {
		t.Fatal("not a first board: sealed 3 days ago")
	}

	s = firstBoardSnap()
	s.Today.Open = 11.77
	if fb.Filter(ctx, s) {
		t.Fatal("gap 7% is above GapMax")
	}

	s = firstBoardSnap()
	s.Today.Close = 11.15
	if fb.Filter(ctx, s) {
		t.Fatal("price below open is not weak-to-strong")
	}

	s = firstBoardSnap()
	s.Bars[29].Amount = 1.2e8
	if fb.Filter(ctx, s) {
		t.Fatal("amount ratio 1.2 is below 1.5")
	}

	s = firstBoardSnap()
	s.Bars[29].High = 10.9
	s.Bars[29].Close = 10.9
	if fb.Filter(ctx, s) {
		t.Fatal("did not touch limit up")
	}
}

func TestFirstBoardPremarketAndBrokenBoard(t *testing.T) {
	fb := NewFirstBoard(DefaultFirstBoard(), DefaultPrice())
	s := firstBoardSnap()
	s.Today = nil
	if !fb.Filter(ctx, s) {
		t.Fatal("pre-market pool should keep the setup")
	}
	if _, ok := fb.EntryPlan(ctx, s); ok {
		t.Fatal("no entry before the open")
	}
	s = firstBoardSnap()
	s.Bars[29].Close = 10.7
	if !fb.Filter(ctx, s) {
		t.Fatal("broken board is still a setup")
	}
	sealed := fb.Score(ctx, firstBoardSnap())
	if broken := fb.Score(ctx, s); broken >= sealed {
		t.Fatalf("broken %.2f should score below sealed %.2f", broken, sealed)
	}
}

func TestPullbackHit(t *testing.T) {
	pd := NewPullback(DefaultPullback(), DefaultPrice())
	s := pullbackSnap()
	if !pd.Filter(ctx, s) {
		t.Fatal("filter should pass")
	}
	score := pd.Score(ctx, s)
	if score < 60 || score > 100 {
		t.Fatalf("score=%.2f", score)
	}
	plan, ok := pd.EntryPlan(ctx, s)
	if !ok || plan.Price != 12.85 || plan.HoldDays != 5 {
		t.Fatalf("plan=%+v ok=%v", plan, ok)
	}
}

func TestPullbackRejects(t *testing.T) {
	pd := NewPullback(DefaultPullback(), DefaultPrice())

	s := pullbackSnap()
	s.Bars[29].Volume = 9e6
	if pd.Filter(ctx, s) {
		t.Fatal("volume did not shrink")
	}

	s = pullbackSnap()
	s.Today.Close = 12.62
	if pd.Filter(ctx, s) {
		t.Fatal("no rebound from the low")
	}

	s = pullbackSnap()
	s.Today.High = 14.5
	if pd.Filter(ctx, s) {
		t.Fatal("today is a new high, not a pullback")
	}

	s = pullbackSnap()
	s.Today.Close = 11.9
	s.Today.Low = 11.7
	if pd.Filter(ctx, s) {
		t.Fatal("pulled back too far from MA10")
	}
}

func TestHardFilter(t *testing.T) {
	p := DefaultFunnel()
	ok := firstBoardSnap()
	if r := HardFilter(ok, p, nil); r != "" {
		t.Fatalf("unexpected drop %s", r)
	}
	cases := map[string]func(*Snapshot){
		DropST:        func(s *Snapshot) { s.ST = true },
		DropSuspended: func(s *Snapshot) { s.Suspended = true },
		DropNew:       func(s *Snapshot) { s.ListDate = asOf.AddDate(0, 0, -30) },
		DropAmount:    func(s *Snapshot) { s.Bars[len(s.Bars)-1].Amount = 4e7 },
		DropLimitUp:   func(s *Snapshot) { s.Today.Close = 12.10 },
		DropNoData:    func(s *Snapshot) { s.Bars = s.Bars[:1] },
	}
	for want, mutate := range cases {
		s := firstBoardSnap()
		s.Bars = append([]Bar(nil), s.Bars...)
		today := *s.Today
		s.Today = &today
		mutate(&s)
		if got := HardFilter(s, p, nil); got != want {
			t.Fatalf("%s: got %q", want, got)
		}
	}
	black := func(sym string) bool { return sym == "600000.SH" }
	if got := HardFilter(firstBoardSnap(), p, black); got != DropBlacklist {
		t.Fatalf("blacklist: got %q", got)
	}
}

func TestScreen(t *testing.T) {
	strategies := Templates(nil)
	fb := firstBoardSnap()
	pd := pullbackSnap()
	st := firstBoardSnap()
	st.Symbol, st.ST = "600001.SH", true
	none := firstBoardSnap()
	none.Symbol = "600002.SH"
	none.Bars = flat(30, 10)
	p := DefaultFunnel()
	res := Screen(ctx, []Snapshot{none, pd, st, fb}, strategies, p, nil)
	if res.Matched != 2 || len(res.Ranked) != 2 || res.Dropped[DropST] != 1 {
		t.Fatalf("res=%+v", res)
	}
	if res.Ranked[0].Score < res.Ranked[1].Score {
		t.Fatal("not sorted by score")
	}
	names := map[string]string{}
	for _, c := range res.Ranked {
		names[c.Snapshot.Symbol] = c.Strategy.Name()
	}
	if names["600000.SH"] != NameFirstBoard || names["000001.SZ"] != NamePullback {
		t.Fatalf("names=%v", names)
	}
	p.RuleTopN = 1
	if res := Screen(ctx, []Snapshot{pd, fb}, strategies, p, nil); len(res.Ranked) != 1 || res.Matched != 2 {
		t.Fatalf("top-n cut failed: %+v", res)
	}
}

func TestFuse(t *testing.T) {
	p := DefaultFuse()
	score, degraded := p.Fuse(80, nil, StageWarm)
	if !degraded || score != 80 {
		t.Fatalf("degraded score=%v", score)
	}
	ai := 90.0
	score, degraded = p.Fuse(80, &ai, StageWarm)
	near(t, "fused", score, 0.4*80+0.6*90, 1e-9)
	if degraded {
		t.Fatal("should not be degraded")
	}
	score, _ = p.Fuse(80, &ai, StageIce)
	near(t, "ice", score, 0.4*80*0.8+0.6*90, 1e-9)
	score, _ = p.Fuse(100, nil, StageHot)
	if score != 100 {
		t.Fatalf("score must be capped at 100, got %v", score)
	}
	if sel, high := p.Selected(75); !sel || high {
		t.Fatal("75 selects but is not high value")
	}
	if sel, high := p.Selected(85); !sel || !high {
		t.Fatal("85 is high value")
	}
	if sel, _ := p.Selected(74.99); sel {
		t.Fatal("74.99 is below threshold")
	}
}

func TestPriceMainBoard(t *testing.T) {
	p := DefaultPrice()
	lv, err := p.Buy(firstBoardSnap(), 11.45)
	if err != nil {
		t.Fatal(err)
	}
	// ATR = (13×0.2 + 1.0) / 14；止盈 11.45+3×ATR=12.22 超过涨停 12.10，被夹住。
	near(t, "atr", lv.ATR, 3.6/14, 1e-9)
	if lv.LimitUp != 12.10 || lv.LimitDown != 9.90 {
		t.Fatalf("limits %v %v", lv.LimitUp, lv.LimitDown)
	}
	if lv.Entry != 11.45 || lv.EntryLow != 11.39 || lv.EntryHigh != 11.51 || lv.StopLoss != 10.94 || lv.TakeProfit != 12.10 {
		t.Fatalf("levels=%+v", lv)
	}
}

func TestPriceMaxStopAndBoards(t *testing.T) {
	p := DefaultPrice()
	s := firstBoardSnap()
	s.Symbol = "300750.SZ"
	for i := range s.Bars {
		s.Bars[i].High += 0.4
		s.Bars[i].Low -= 0.4
	}
	lv, err := p.Buy(s, 11.45)
	if err != nil {
		t.Fatal(err)
	}
	// ATR 约 0.94，2×ATR 超过 7%，止损被抬到 11.45×0.93=10.6485 → 10.65。创业板涨停 13.20。
	if lv.StopLoss != 10.65 || lv.LimitUp != 13.20 {
		t.Fatalf("levels=%+v", lv)
	}
	if loss := 1 - lv.StopLoss/lv.Entry; loss > 0.07+1e-9 {
		t.Fatalf("stop loss %.4f exceeds 7%%", loss)
	}

	// 主板 ST 在 2026-07-06 之前是 5%，之后与主板一样是 10%。
	s = firstBoardSnap()
	s.ST = true
	s.AsOf = time.Date(2026, 6, 1, 10, 0, 0, 0, asOf.Location())
	lv, err = p.Buy(s, 11.45)
	if err != nil {
		t.Fatal(err)
	}
	if lv.LimitUp != 11.55 || lv.TakeProfit != 11.55 || lv.EntryHigh != 11.51 {
		t.Fatalf("ST levels=%+v", lv)
	}
	s.AsOf = asOf
	if lv, _ = p.Buy(s, 11.45); lv.LimitUp != 12.10 {
		t.Fatalf("ST after 2026-07-06 limit up=%v", lv.LimitUp)
	}

	if _, err := p.Buy(firstBoardSnap(), 12.10); !errors.Is(err, ErrLevels) {
		t.Fatalf("entry at limit up must collapse, err=%v", err)
	}
	short := firstBoardSnap()
	short.Bars = short.Bars[len(short.Bars)-10:]
	if _, err := p.Buy(short, 11.45); !errors.Is(err, ErrNoATR) {
		t.Fatalf("err=%v", err)
	}
	if p.PositionPct(true) != 0.05 || math.Abs(p.PositionPct(false)-0.03) > 1e-12 {
		t.Fatal("position pct")
	}
}

func TestExit(t *testing.T) {
	p := DefaultPrice()
	s := firstBoardSnap()
	pos := Position{Symbol: s.Symbol, Quantity: 1000, Available: 1000, StopLoss: 11.5, TakeProfit: 12, HoldDays: 1}
	s.NegativeNews = true
	plan, ok := Exit(s, pos, 2, p)
	if !ok || plan.ExitKind != ExitStopLoss || plan.Shares != 1000 || plan.Side != SideSell {
		t.Fatalf("stop loss first: %+v", plan)
	}
	// 11.45 × 0.995 = 11.39
	if plan.Price != 11.39 {
		t.Fatalf("sell price %v", plan.Price)
	}
	pos.StopLoss = 10
	if plan, _ := Exit(s, pos, 2, p); plan.ExitKind != ExitBadNews {
		t.Fatalf("bad news: %+v", plan)
	}
	s.NegativeNews = false
	pos.TakeProfit = 11.4
	if plan, _ := Exit(s, pos, 2, p); plan.ExitKind != ExitTakeProfit {
		t.Fatalf("take profit: %+v", plan)
	}
	pos.TakeProfit = 12
	s.Stage = StageFade
	if plan, _ := Exit(s, pos, 2, p); plan.ExitKind != ExitSentiment {
		t.Fatalf("sentiment: %+v", plan)
	}
	s.Stage = StageWarm
	if _, ok := Exit(s, pos, 2, p); ok {
		t.Fatal("nothing should trigger")
	}
	pos.HoldDays = 2
	if plan, _ := Exit(s, pos, 2, p); plan.ExitKind != ExitTime {
		t.Fatalf("time stop: %+v", plan)
	}
	if _, ok := Exit(s, pos, 0, p); ok {
		t.Fatal("holdMax 0 disables time stop")
	}
	pos.Available = 0
	if _, ok := Exit(s, pos, 2, p); ok {
		t.Fatal("T+1: nothing to sell")
	}
	pos.Available = 1000
	s.Today = nil
	if _, ok := Exit(s, pos, 2, p); ok {
		t.Fatal("no exit before the open")
	}
}

func TestLoadParams(t *testing.T) {
	get := func(key string) (float64, bool) {
		switch key {
		case "m06.fb.hold_days":
			return 3, true
		case "m06.select_score":
			return 70, true
		case "m06.stage_coef.ICE":
			return 0.5, true
		}
		return 0, false
	}
	list := Templates(get)
	fb := ByName(list, NameFirstBoard).(*FirstBoard)
	if fb.P.HoldDays != 3 || fb.P.AmountRatio != 1.5 {
		t.Fatalf("fb params %+v", fb.P)
	}
	f := LoadFuse(get)
	if f.SelectScore != 70 || f.Coef(StageIce) != 0.5 || f.Coef(StageWarm) != 1 {
		t.Fatalf("fuse params %+v", f)
	}
	if ByName(list, "nope") != nil {
		t.Fatal("unknown template")
	}
}

func TestTemplatesRegistered(t *testing.T) {
	spec, ok := Find(NameFirstBoard)
	if !ok {
		t.Fatal("first board not registered")
	}
	params, err := spec.Resolve(map[string]float64{"hold_days": 3, "stop_atr": 1.5})
	if err != nil {
		t.Fatal(err)
	}
	st, err := spec.New(params)
	if err != nil {
		t.Fatal(err)
	}
	fb := st.(*FirstBoard)
	if fb.P.HoldDays != 3 || fb.Price.StopATR != 1.5 || fb.P.AmountRatio != 1.5 {
		t.Fatalf("fb=%+v price=%+v", fb.P, fb.Price)
	}
	// 注册表建出的实例与实盘 Templates 默认值一致。
	spec, ok = Find(NamePullback)
	if !ok {
		t.Fatal("pullback not registered")
	}
	params, _ = spec.Resolve(nil)
	st, _ = spec.New(params)
	if !st.Filter(ctx, pullbackSnap()) {
		t.Fatal("registered pullback should match the fixture")
	}
}
