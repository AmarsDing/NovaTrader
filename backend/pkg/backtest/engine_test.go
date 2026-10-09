package backtest

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"server/pkg/rules"
	_ "server/pkg/rules/refstrat"
	"server/pkg/tradecal"
)

// script 是测试策略：对 buy 里的代码买 shares 股（最多 buys 次），持仓一可卖就卖。
type script struct {
	buy    map[string]bool
	shares int
	buys   int
	limit  float64
}

var cur = &script{}

func init() {
	rules.Register(rules.Spec{Name: "t_script", New: func(map[string]float64) (rules.Strategy, error) { return cur, nil }})
}

func (s *script) Name() string                                     { return "t_script" }
func (s *script) Filter(_ context.Context, sn rules.Snapshot) bool { return s.buy[sn.Symbol] }
func (s *script) Score(context.Context, rules.Snapshot) float64    { return 1 }
func (s *script) EntryPlan(_ context.Context, sn rules.Snapshot) (rules.Plan, bool) {
	if sn.Today == nil || !s.buy[sn.Symbol] || s.buys <= 0 {
		return rules.Plan{}, false
	}
	s.buys--
	return rules.Plan{Side: rules.SideBuy, Shares: s.shares, Price: s.limit, Reason: "test buy"}, true
}
func (s *script) ExitPlan(_ context.Context, _ rules.Snapshot, pos rules.Position) (rules.Plan, bool) {
	if pos.Available <= 0 {
		return rules.Plan{}, false
	}
	return rules.Plan{Side: rules.SideSell, Reason: "test sell"}, true
}

func day(s string) time.Time { d, _ := ParseDay(s); return d }

func minute(d time.Time, h, m int, o, hi, lo, c float64, v int64) MinBar {
	return MinBar{Time: time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, tradecal.Shanghai()), Open: o, High: hi, Low: lo, Close: c, Volume: v, Amount: c * float64(v)}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

const sym = "600000.SH"

func twoDays() *Memory {
	d0, d1, d2 := day("2026-03-02"), day("2026-03-03"), day("2026-03-04")
	return &Memory{
		Days: []time.Time{d0, d1, d2},
		Daily: map[string][]DayBar{sym: {
			{Day: d0, Open: 10, High: 10, Low: 10, Close: 10, Volume: 1e6, Amount: 1e7, Adj: 1},
			{Day: d1, Open: 10, High: 10.1, Low: 10, Close: 10.1, Volume: 2e5, Amount: 2e6, Adj: 1, PreClose: 10},
			{Day: d2, Open: 10.2, High: 10.3, Low: 10.2, Close: 10.3, Volume: 2e5, Amount: 2e6, Adj: 1, PreClose: 10.1},
		}},
		Minute: map[string]map[string][]MinBar{
			"2026-03-03": {sym: {
				minute(d1, 9, 30, 10, 10, 10, 10, 100000),
				minute(d1, 9, 31, 10.05, 10.1, 10, 10.1, 100000),
				minute(d1, 9, 32, 10.1, 10.1, 10.1, 10.1, 100000),
			}},
			"2026-03-04": {sym: {
				minute(d2, 9, 30, 10.2, 10.2, 10.2, 10.2, 100000),
				minute(d2, 9, 31, 10.3, 10.3, 10.2, 10.3, 100000),
				minute(d2, 9, 32, 10.3, 10.3, 10.3, 10.3, 100000),
			}},
		},
	}
}

func scriptCfg(start, end string) Config {
	c := DefaultConfig()
	c.Strategy, c.Start, c.End = "t_script", start, end
	return c
}

// 手工推演：9:31 开盘 10.05 + 1 跳 = 10.06 买 1000 股；当日不能卖；次日 9:31 开盘 10.30 − 1 跳 = 10.29 卖出。
func TestFeesAndT1(t *testing.T) {
	cur = &script{buy: map[string]bool{sym: true}, shares: 1000, buys: 1}
	res, err := Run(context.Background(), scriptCfg("2026-03-03", "2026-03-04"), twoDays(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 2 {
		t.Fatalf("trades = %+v", res.Trades)
	}
	b, s := res.Trades[0], res.Trades[1]
	if b.Side != "buy" || !near(b.Price, 10.06) || b.Qty != 1000 || !near(b.Fee, 5.10) {
		t.Fatalf("buy = %+v，佣金 2.515 按最低 5 元，过户费 0.1006，合计 5.10", b)
	}
	if b.Time.Hour() != 9 || b.Time.Minute() != 31 {
		t.Fatalf("buy time = %v，9:30 走完才出信号，9:31 成交", b.Time)
	}
	if dayKey(s.Time) != "2026-03-04" {
		t.Fatalf("sell on %v，T+1 当日不能卖", s.Time)
	}
	if s.Side != "sell" || !near(s.Price, 10.29) || !near(s.Fee, 10.25) {
		t.Fatalf("sell = %+v，佣金 5 + 印花税 5.145 + 过户费 0.1029 = 10.25", s)
	}
	if len(res.Rounds) != 1 || !near(res.Rounds[0].PnL, 214.65) || res.Rounds[0].HoldDays != 1 {
		t.Fatalf("round = %+v，10290 − 10060 − 5.10 − 10.25 = 214.65", res.Rounds)
	}
	if last := res.Equity[len(res.Equity)-1]; !near(last.Equity, 1_000_214.65) {
		t.Fatalf("equity = %v", last.Equity)
	}
	if !near(res.Equity[0].Equity, 1_000_000-10060-5.10+1000*10.1) {
		t.Fatalf("day1 equity = %v，按收盘 10.1 估值", res.Equity[0].Equity)
	}
}

// 前收 10，涨停 11.00。一字板买不到；打开过的 K 线在 opened 口径下按 11.00 成交，never 口径下不成交。
func TestLimitUpLock(t *testing.T) {
	d0, d1 := day("2026-03-02"), day("2026-03-03")
	mk := func(open bool) *Memory {
		bars := []MinBar{
			minute(d1, 9, 30, 11, 11, 11, 11, 50000),
			minute(d1, 9, 31, 11, 11, 11, 11, 50000),
		}
		if open {
			bars = append(bars, minute(d1, 9, 32, 11, 11, 10.9, 11, 50000))
		}
		return &Memory{
			Days: []time.Time{d0, d1},
			Daily: map[string][]DayBar{sym: {
				{Day: d0, Open: 10, High: 10, Low: 10, Close: 10, Volume: 1e6, Amount: 1e7, Adj: 1},
				{Day: d1, Open: 11, High: 11, Low: 10.9, Close: 11, Volume: 1.5e5, Amount: 1.65e6, Adj: 1, PreClose: 10},
			}},
			Minute: map[string]map[string][]MinBar{"2026-03-03": {sym: bars}},
		}
	}
	cur = &script{buy: map[string]bool{sym: true}, shares: 1000, buys: 10}
	res, err := Run(context.Background(), scriptCfg("2026-03-03", "2026-03-03"), mk(false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 0 || res.RejectWhy["涨停封板"] == 0 {
		t.Fatalf("一字板不应成交: trades=%v rejects=%v", res.Trades, res.RejectWhy)
	}

	cur = &script{buy: map[string]bool{sym: true}, shares: 1000, buys: 10}
	res, err = Run(context.Background(), scriptCfg("2026-03-03", "2026-03-03"), mk(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 || !near(res.Trades[0].Price, 11) || res.Trades[0].Time.Minute() != 32 {
		t.Fatalf("opened 口径应在 9:32 按涨停价 11.00 成交: %+v", res.Trades)
	}

	cur = &script{buy: map[string]bool{sym: true}, shares: 1000, buys: 10}
	cfg := scriptCfg("2026-03-03", "2026-03-03")
	cfg.LimitFill = "never"
	res, err = Run(context.Background(), cfg, mk(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 0 || res.RejectWhy["涨停不排队"] == 0 {
		t.Fatalf("never 口径不应成交: %+v %v", res.Trades, res.RejectWhy)
	}
}

// 限价单和成交量上限：成交量 5000 股的 10% = 500 股。
func TestVolumeCapAndLimit(t *testing.T) {
	m := twoDays()
	d1 := day("2026-03-03")
	m.Minute["2026-03-03"][sym][1] = minute(d1, 9, 31, 10.05, 10.1, 10, 10.1, 5000)
	cur = &script{buy: map[string]bool{sym: true}, shares: 1000, buys: 1}
	res, err := Run(context.Background(), scriptCfg("2026-03-03", "2026-03-03"), m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 || res.Trades[0].Qty != 500 {
		t.Fatalf("应只成交 500 股: %+v", res.Trades)
	}

	cur = &script{buy: map[string]bool{sym: true}, shares: 1000, buys: 1, limit: 10.05}
	res, err = Run(context.Background(), scriptCfg("2026-03-03", "2026-03-03"), twoDays(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 0 || res.RejectWhy["价格未到"] != 1 {
		t.Fatalf("限价 10.05 低于 10.06 不成交: %+v %v", res.Trades, res.RejectWhy)
	}
}

// 日线模式：D1 收盘出信号，D2 开盘买，D2 收盘起可卖并出卖出信号；D3 复权因子 1 → 1.1，先按 1000 × 10.2 × (1 − 1/1.1) 入账，再开盘卖。
func TestDailyModeAndExRights(t *testing.T) {
	d := []time.Time{day("2026-03-02"), day("2026-03-03"), day("2026-03-04"), day("2026-03-05"), day("2026-03-06")}
	m := &Memory{Days: d, Daily: map[string][]DayBar{sym: {
		{Day: d[0], Open: 10, High: 10, Low: 10, Close: 10, Volume: 1e6, Amount: 1e7, Adj: 1},
		{Day: d[1], Open: 10, High: 10.1, Low: 10, Close: 10, Volume: 1e6, Amount: 1e7, Adj: 1, PreClose: 10},
		{Day: d[2], Open: 10.1, High: 10.3, Low: 10.1, Close: 10.2, Volume: 1e6, Amount: 1e7, Adj: 1, PreClose: 10},
		{Day: d[3], Open: 9.3, High: 9.4, Low: 9.2, Close: 9.3, Volume: 1e6, Amount: 1e7, Adj: 1.1, PreClose: 9.27},
		{Day: d[4], Open: 9.4, High: 9.5, Low: 9.3, Close: 9.4, Volume: 1e6, Amount: 1e7, Adj: 1.1, PreClose: 9.3},
	}}}
	cur = &script{buy: map[string]bool{sym: true}, shares: 1000, buys: 1}
	cfg := scriptCfg("2026-03-03", "2026-03-06")
	cfg.Freq = Freq1d
	res, err := Run(context.Background(), cfg, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 3 {
		t.Fatalf("trades = %+v", res.Trades)
	}
	buy, div, sell := res.Trades[0], res.Trades[1], res.Trades[2]
	if buy.Side != "buy" || dayKey(buy.Time) != "2026-03-04" || !near(buy.Price, 10.11) {
		t.Fatalf("buy = %+v", buy)
	}
	credit := math.Round(1000*10.2*(1-1/1.1)*100) / 100
	if div.Side != "dividend" || dayKey(div.Time) != "2026-03-05" || !near(div.Amount, credit) {
		t.Fatalf("dividend = %+v, want %.2f", div, credit)
	}
	if sell.Side != "sell" || dayKey(sell.Time) != "2026-03-05" || !near(sell.Price, 9.29) {
		t.Fatalf("sell = %+v", sell)
	}
	r := res.Rounds[0]
	want := round2(sell.Amount - buy.Amount - buy.Fee - sell.Fee + credit)
	if !near(r.PnL, want) {
		t.Fatalf("pnl = %v want %v", r.PnL, want)
	}
}

// market 生成可复现的随机行情：n 只主板股票，days 个交易日，每天 240 根分钟线。日线由分钟线聚合。
func market(n, days int, seed int64, minutes bool) *Memory {
	rng := rand.New(rand.NewSource(seed))
	m := &Memory{Daily: map[string][]DayBar{}, Minute: map[string]map[string][]MinBar{}}
	d := day("2025-06-02")
	for len(m.Days) < days {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			m.Days = append(m.Days, d)
		}
		d = d.AddDate(0, 0, 1)
	}
	codes := []string{"600000.SH", "600001.SH", "600002.SH", "000001.SZ", "000002.SZ", "600003.SH", "000003.SZ", "600004.SH"}
	for k := 0; k < n; k++ {
		code := codes[k]
		px := 10 + float64(k)
		adjF := 1.0
		for i, dd := range m.Days {
			pre := px
			drift := rng.NormFloat64() * 0.025
			if rng.Float64() < 0.08 {
				drift += 0.05
			}
			up, down := math.Round(pre*1.1*100)/100, math.Round(pre*0.9*100)/100
			bar := DayBar{Day: dd, Adj: adjF, PreClose: pre}
			var mins []MinBar
			p := pre
			for j := 0; j < 240; j++ {
				h, mm := 9, 30+j
				if j >= 120 {
					h, mm = 13, j-120
				}
				h += mm / 60
				mm %= 60
				o := p
				p = math.Round(math.Min(up, math.Max(down, p*(1+drift/240+rng.NormFloat64()*0.002)))*100) / 100
				hi, lo := math.Max(o, p), math.Min(o, p)
				v := int64(20000 + rng.Intn(80000))
				mins = append(mins, minute(dd, h, mm, o, hi, lo, p, v))
				if j == 0 {
					bar.Open, bar.High, bar.Low = o, hi, lo
				}
				bar.High, bar.Low = math.Max(bar.High, hi), math.Min(bar.Low, lo)
				bar.Volume += v
				bar.Amount += p * float64(v) * 100
			}
			bar.Close = p
			m.Daily[code] = append(m.Daily[code], bar)
			if minutes {
				if m.Minute[dayKey(dd)] == nil {
					m.Minute[dayKey(dd)] = map[string][]MinBar{}
				}
				m.Minute[dayKey(dd)][code] = mins
			}
			px = p
			if i == days/3 && k == 0 {
				adjF *= 1.05
				px = math.Round(px/1.05*100) / 100
			}
		}
	}
	return m
}

func breakoutCfg(m *Memory, freq string, from int) Config {
	c := DefaultConfig()
	c.Strategy, c.Freq, c.Start, c.End = "breakout_ref", freq, dayKey(m.Days[from]), dayKey(m.Days[len(m.Days)-1])
	return c
}

func TestRerunIdentical(t *testing.T) {
	m := market(5, 90, 7, true)
	cfg := breakoutCfg(m, Freq1m, 30)
	a, err := Run(context.Background(), cfg, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Trades) == 0 {
		t.Fatal("合成行情没有产生成交，测试无效")
	}
	b, err := Run(context.Background(), cfg, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.DataHash != b.DataHash || a.FillsHash != b.FillsHash {
		t.Fatalf("重跑不一致: %s/%s vs %s/%s", a.DataHash, a.FillsHash, b.DataHash, b.FillsHash)
	}
	cache, err := NewCache(context.Background(), m, normalized(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Run(context.Background(), cfg, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.FillsHash != a.FillsHash {
		t.Fatal("经缓存运行结果不同")
	}
	for _, tr := range a.Trades {
		if tr.Side == "sell" {
			continue
		}
		if tr.Side == "buy" && tr.Time.Hour() == 9 && tr.Time.Minute() == 30 {
			t.Fatalf("9:30 那根走完前不可能有信号: %+v", tr)
		}
	}
}

func normalized(t *testing.T, cfg Config) Config {
	t.Helper()
	if _, _, err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLookahead(t *testing.T) {
	for _, freq := range []string{Freq1d, Freq1m} {
		m := market(5, 90, 11, freq == Freq1m)
		cfg := breakoutCfg(m, freq, 30)
		base, err := Run(context.Background(), cfg, m, nil)
		if err != nil {
			t.Fatal(err)
		}
		r, err := LookaheadCheck(context.Background(), cfg, m, base)
		if err != nil {
			t.Fatal(err)
		}
		if !r.Pass || r.DaysCompared == 0 {
			t.Fatalf("%s: %+v", freq, r)
		}
		if r.TradesCompared == 0 {
			t.Fatalf("%s: 切点前没有成交，测试无效", freq)
		}
	}
}

// leaky 偷看下一交易日收盘价，截断自检必须能抓到。
type leaky struct{ m *Memory }

func (l leaky) Name() string                                  { return "t_leaky" }
func (l leaky) Filter(context.Context, rules.Snapshot) bool   { return true }
func (l leaky) Score(context.Context, rules.Snapshot) float64 { return 1 }
func (l leaky) next(s rules.Snapshot) float64 {
	for i, b := range l.m.Daily[s.Symbol] {
		if b.Day.Equal(Day(s.AsOf)) && i+1 < len(l.m.Daily[s.Symbol]) {
			return l.m.Daily[s.Symbol][i+1].Close
		}
	}
	return 0
}
func (l leaky) EntryPlan(_ context.Context, s rules.Snapshot) (rules.Plan, bool) {
	if n := l.next(s); n > s.Last()*1.02 {
		return rules.Plan{Side: rules.SideBuy, Reason: "leak"}, true
	}
	return rules.Plan{}, false
}
func (l leaky) ExitPlan(_ context.Context, _ rules.Snapshot, p rules.Position) (rules.Plan, bool) {
	return rules.Plan{Side: rules.SideSell}, p.Available > 0
}

var leakSrc *Memory

func init() {
	rules.Register(rules.Spec{Name: "t_leaky", New: func(map[string]float64) (rules.Strategy, error) { return leaky{m: leakSrc}, nil }})
}

func TestLookaheadCatchesLeak(t *testing.T) {
	m := market(5, 90, 3, false)
	leakSrc = m
	cfg := DefaultConfig()
	cfg.Strategy, cfg.Freq, cfg.Start, cfg.End = "t_leaky", Freq1d, dayKey(m.Days[30]), dayKey(m.Days[89])
	base, err := Run(context.Background(), cfg, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	cut := base.Equity[len(base.Equity)/2].Day
	p := &perturbed{Source: m, cut: cut, seed: base.Config.Seed}
	daily, _ := p.DailyBars(context.Background(), m.Days[0], m.Days[len(m.Days)-1])
	leakSrc = &Memory{Days: m.Days, Daily: daily}
	r, err := LookaheadCheck(context.Background(), cfg, m, base)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pass {
		t.Fatalf("偷看未来的策略没被发现: %+v", r)
	}
}

func TestMetrics(t *testing.T) {
	eq := []EquityPoint{
		{Day: day("2026-01-05"), Equity: 110},
		{Day: day("2026-01-06"), Equity: 99},
		{Day: day("2026-01-07"), Equity: 121},
	}
	rounds := []Round{{PnL: 20, Return: 0.2}, {PnL: -10, Return: -0.1}, {PnL: 10, Return: 0.1}}
	m := Compute(100, 0, eq, rounds, []Trade{{Side: "buy", Amount: 100}, {Side: "sell", Amount: 110}})
	if !near(m.TotalReturn, 0.21) || !near(m.MaxDrawdown, 0.1) {
		t.Fatalf("%+v", m)
	}
	if !near(m.WinRate, 2.0/3) || !near(m.ProfitFactor, 1.5) || !near(m.Expectancy, 0.2/3) {
		t.Fatalf("win %v pf %v exp %v", m.WinRate, m.ProfitFactor, m.Expectancy)
	}
	if !near(m.AnnualReturn, math.Pow(1.21, 252.0/3)-1) {
		t.Fatalf("annual %v", m.AnnualReturn)
	}
	rets := []float64{0.1, -0.1, 121.0/99 - 1}
	mean, sd := meanStd(rets)
	if !near(m.Sharpe, mean/sd*math.Sqrt(252)) {
		t.Fatalf("sharpe %v", m.Sharpe)
	}
	if !near(m.Turnover, 105/(330.0/3)*252/3) {
		t.Fatalf("turnover %v", m.Turnover)
	}
}

func TestOptimizeAndWalkForward(t *testing.T) {
	m := market(5, 140, 5, false)
	cfg := breakoutCfg(m, Freq1d, 20)
	sc := SearchConfig{Method: "grid", MinTrades: 1, Workers: 3, Ranges: map[string]Range{
		"lookback": {Min: 10, Max: 20, Step: 5},
		"max_hold": {Min: 1, Max: 3, Step: 1},
	}}
	a, oos, err := Optimize(context.Background(), cfg, sc, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Points) != 9 || a.Best == nil || oos == nil {
		t.Fatalf("%+v", a)
	}
	sc.Workers = 1
	b, _, err := Optimize(context.Background(), cfg, sc, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.Points {
		if a.Points[i].Objective != b.Points[i].Objective {
			t.Fatal("并发数不同结果不同")
		}
	}
	if a.ISEnd >= a.OOSStart {
		t.Fatalf("样本内外重叠: %s %s", a.ISEnd, a.OOSStart)
	}

	sc.TrainDays, sc.TestDays = 40, 20
	wf, comb, err := WalkForward(context.Background(), cfg, sc, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Windows) != (120-40)/20 {
		t.Fatalf("windows = %d", len(wf.Windows))
	}
	for i := 1; i < len(wf.Windows); i++ {
		if wf.Windows[i].TestStart <= wf.Windows[i-1].TestEnd {
			t.Fatal("测试窗重叠")
		}
	}
	if len(comb.Equity) != 4*20 {
		t.Fatalf("拼接权益 %d 天", len(comb.Equity))
	}

	_, err = func() (*OptimizeResult, error) {
		r, _, err := Optimize(context.Background(), cfg, SearchConfig{Method: "grid", MaxRuns: 5}, m, nil)
		return r, err
	}()
	if err == nil {
		t.Fatal("网格超过 max_runs 应报错")
	}
}

func TestEvolveBounded(t *testing.T) {
	spec, _ := rules.Find("breakout_ref")
	cfg := DefaultConfig()
	cfg.Strategy = "breakout_ref"
	base, _ := spec.Resolve(nil)
	cands := Neighbors(spec, base, 20, 1)
	if len(cands) == 0 || len(cands) > 20 {
		t.Fatalf("cands = %d", len(cands))
	}
	for _, c := range cands {
		for _, p := range spec.Params {
			if math.Abs(c[p.Name]-base[p.Name]) > p.Step+1e-9 || c[p.Name] < p.Min || c[p.Name] > p.Max {
				t.Fatalf("候选越界: %v", c)
			}
		}
	}
	m := market(5, 120, 9, false)
	cfg.Freq, cfg.Start, cfg.End = Freq1d, dayKey(m.Days[20]), dayKey(m.Days[119])
	r, err := Evolve(context.Background(), cfg, SearchConfig{MinTrades: 1}, cands, 60, 40, m)
	if err != nil {
		t.Fatal(err)
	}
	if r.Reason == "" || r.ISEnd >= r.OOSStart {
		t.Fatalf("%+v", r)
	}
}

var (
	_ Pricer = (*rules.FirstBoard)(nil)
	_ Pricer = (*rules.Pullback)(nil)
)

// M06 两个模板按注册表名称可直接回测；持仓带上下单时算出的止损止盈。
func TestM06Templates(t *testing.T) {
	m := market(8, 100, 21, true)
	for _, name := range []string{rules.NameFirstBoard, rules.NamePullback} {
		cfg := breakoutCfg(m, Freq1m, 40)
		cfg.Strategy = name
		res, err := Run(context.Background(), cfg, m, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s: %d 笔成交，%d 个回合，拒单 %v", name, len(res.Trades), len(res.Rounds), res.RejectWhy)
	}
}

func TestParamKey(t *testing.T) {
	fb, _ := rules.Find(rules.NameFirstBoard)
	if fb.ParamKey("hold_days") != "m06.fb.hold_days" || fb.ParamKey("stop_atr") != "m06.stop_atr" {
		t.Fatal(fb.ParamKey("hold_days"), fb.ParamKey("stop_atr"))
	}
	br, _ := rules.Find("breakout_ref")
	if br.ParamKey("lookback") != "breakout_ref.lookback" {
		t.Fatal(br.ParamKey("lookback"))
	}
}

func TestMonteCarlo(t *testing.T) {
	var eq []EquityPoint
	v := 100.0
	for i := 0; i < 100; i++ {
		v *= 1 + 0.001*float64(i%7-3)
		eq = append(eq, EquityPoint{Equity: v})
	}
	a := MonteCarlo(100, eq, 500, 5, 1, 0.1)
	b := MonteCarlo(100, eq, 500, 5, 1, 0.1)
	if *a != *b {
		t.Fatal("同种子结果不同")
	}
	if !(a.ReturnP5 <= a.ReturnP50 && a.ReturnP50 <= a.ReturnP95) || a.LossProb < 0 || a.LossProb > 1 {
		t.Fatalf("%+v", a)
	}
}
