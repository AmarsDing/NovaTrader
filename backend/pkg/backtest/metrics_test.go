package backtest

import (
	"math"
	"testing"
)

// 设计文档 §6 指标口径的包级钉死。TestMetrics（engine_test.go）已钉累计/年化/夏普/胜率/
// 盈亏比/期望/换手的基础算式；本文件补 §6 表里其余承重口径：卡玛、分年、峰值/谷底日、
// 最差回合、基准与超额、平均持仓天数，以及它们的零值/短路语义。

func TestComputeCalmarAndDays(t *testing.T) {
	// 设计 :187 卡玛 = 年化 / 最大回撤。
	eq := []EquityPoint{
		{Day: day("2026-01-05"), Equity: 110},
		{Day: day("2026-01-06"), Equity: 99},
		{Day: day("2026-01-07"), Equity: 121},
	}
	m := Compute(100, 0, eq, nil, []Trade{{Side: "buy", Amount: 100}, {Side: "sell", Amount: 110}, {Side: "dividend", Amount: 2.5}})
	if m.Days != 3 || m.Rounds != 0 {
		t.Fatalf("days/rounds: %+v", m)
	}
	// 除权入账不是成交：buy/sell 才计 Fills（设计 :196 回合盈亏含除权入账，但它不是一笔撮合）。
	if m.Fills != 2 {
		t.Fatalf("fills = %d, dividend 不计", m.Fills)
	}
	if m.FinalEquity != 121 {
		t.Fatalf("final = %v", m.FinalEquity)
	}
	annual := math.Pow(1.21, 252.0/3) - 1
	if !near(m.Calmar, annual/0.1) {
		t.Fatalf("calmar = %v, want annual/mdd = %v", m.Calmar, annual/0.1)
	}

	// 单调上行没有回撤：分母为 0 时卡玛静默为 0，不输出 Inf。
	up := []EquityPoint{{Day: day("2026-01-05"), Equity: 110}, {Day: day("2026-01-06"), Equity: 121}}
	m2 := Compute(100, 0, up, nil, nil)
	if m2.MaxDrawdown != 0 || m2.Calmar != 0 {
		t.Fatalf("zero drawdown calmar must stay 0: %+v", m2)
	}
}

func TestComputeShortCircuitKeepsCounts(t *testing.T) {
	// 空权益/非正初始资金直接短路（metrics.go :52）：回合数、天数、成交笔数先于短路落下，
	// 其余口径一律零值——空输入不猜数。
	rounds := []Round{{}, {}}
	trades := []Trade{{Side: "buy", Amount: 1}}
	m := Compute(100, 0, nil, rounds, trades)
	if m.Rounds != 2 || m.Days != 0 || m.Fills != 1 {
		t.Fatalf("counts before short circuit: %+v", m)
	}
	if m.TotalReturn != 0 || m.FinalEquity != 0 || m.Sharpe != 0 {
		t.Fatalf("empty equity must zero the metrics: %+v", m)
	}
	// 初始资金非正：FinalEquity 也不落（短路在赋值之前），零口径而不是误用末点权益。
	m2 := Compute(0, 0, []EquityPoint{{Day: day("2026-01-05"), Equity: 50}}, nil, nil)
	if m2.Days != 1 || m2.FinalEquity != 0 || m2.TotalReturn != 0 {
		t.Fatalf("non-positive initial must zero everything incl. final: %+v", m2)
	}
}

func TestComputeTotalWipeout(t *testing.T) {
	// 全损到 0：累计 −100% 时年化钉为 −1（metrics.go :59-63）。==−1 时 Pow(0, 正指数)=0
	// 殊途同归；else 分支真正挡住的是 TotalReturn<−1（负底数 × 非整指数 252/n → NaN）。
	eq := []EquityPoint{
		{Day: day("2026-01-05"), Equity: 50},
		{Day: day("2026-01-06"), Equity: 0},
	}
	m := Compute(100, 0, eq, nil, nil)
	if m.TotalReturn != -1 || m.AnnualReturn != -1 {
		t.Fatalf("wipeout: %+v", m)
	}
	if !near(m.MaxDrawdown, 1) {
		t.Fatalf("mdd = %v", m.MaxDrawdown)
	}
	// 卡玛照常算：−1 / 1 = −1。
	if !near(m.Calmar, -1) {
		t.Fatalf("calmar on wipeout = %v", m.Calmar)
	}
}

func TestMaxDrawdownPeakTroughDates(t *testing.T) {
	// 设计 :185 记峰值日和谷底日。峰值只有在权益创新高后才落到具体交易日。
	eq := []EquityPoint{
		{Day: day("2026-03-02"), Equity: 120}, // 新高峰值
		{Day: day("2026-03-03"), Equity: 90},  // dd .25
		{Day: day("2026-03-04"), Equity: 110}, // 没超过 120，不换峰值
		{Day: day("2026-03-05"), Equity: 60},  // dd .5 ← 最大
	}
	m := Compute(100, 0, eq, nil, nil)
	if !near(m.MaxDrawdown, 0.5) {
		t.Fatalf("mdd = %v", m.MaxDrawdown)
	}
	if !m.DrawdownPeak.Equal(day("2026-03-02")) || !m.DrawdownTrough.Equal(day("2026-03-05")) {
		t.Fatalf("peak %s trough %s", m.DrawdownPeak, m.DrawdownTrough)
	}

	// 权益从未超过初始资金：峰值就是初始资金，没有对应的交易日——峰值日为零值，
	// 谷底日照常（代码口径，metrics.go :143-157，零值由报告端自行解释）。
	down := []EquityPoint{
		{Day: day("2026-03-02"), Equity: 90},
		{Day: day("2026-03-03"), Equity: 80},
	}
	m2 := Compute(100, 0, down, nil, nil)
	if !near(m2.MaxDrawdown, 0.2) || !m2.DrawdownPeak.IsZero() || !m2.DrawdownTrough.Equal(day("2026-03-03")) {
		t.Fatalf("initial-capital peak: %+v", m2)
	}
}

func TestFillDrawdownPerPoint(t *testing.T) {
	eq := []EquityPoint{
		{Day: day("2026-03-02"), Equity: 120},
		{Day: day("2026-03-03"), Equity: 90},
		{Day: day("2026-03-04"), Equity: 130},
		{Day: day("2026-03-05"), Equity: 65},
	}
	FillDrawdown(100, eq)
	want := []float64{0, 0.25, 0, 0.5}
	for i, w := range want {
		if !near(eq[i].Drawdown, w) {
			t.Fatalf("point %d dd = %v, want %v", i, eq[i].Drawdown, w)
		}
	}
}

func TestYearlyCrossYearCarry(t *testing.T) {
	// 设计 :193 分年 + metrics.go :172 首年起点为初始资金、后续年起点为上年最后一个权益。
	eq := []EquityPoint{
		{Day: day("2025-12-30"), Equity: 1100, Benchmark: 1000},
		{Day: day("2025-12-31"), Equity: 1050, Benchmark: 1010},
		{Day: day("2026-01-05"), Equity: 1155, Benchmark: 1050.5},
		{Day: day("2026-01-06"), Equity: 1050, Benchmark: 1060.5},
	}
	rounds := []Round{
		{CloseTime: day("2025-12-31")},
		{CloseTime: day("2026-01-05")},
		{CloseTime: day("2026-01-06")},
	}
	ys := Yearly(1000, eq, rounds)
	if len(ys) != 2 || ys[0].Year != 2025 || ys[1].Year != 2026 {
		t.Fatalf("years: %+v", ys)
	}
	// 2025：1050/1000 − 1 = 5%；年内峰值 1100，回撤 (1100−1050)/1100。
	if !near(ys[0].Return, 0.05) || !near(ys[0].MaxDrawdown, (1100-1050)/1100.0) {
		t.Fatalf("y2025: %+v", ys[0])
	}
	// 2026：起点接上 2025-12-31 的 1050（不是初始资金 1000），收益恰为 0；
	// 年内峰值按 1050 起步重置（每年单独算回撤，峰值不跨年继承）。
	if !near(ys[1].Return, 0) || !near(ys[1].MaxDrawdown, (1155-1050)/1155.0) {
		t.Fatalf("y2026 carry: %+v", ys[1])
	}
	// 回合数按 CloseTime 的自然年归属。
	if ys[0].Rounds != 1 || ys[1].Rounds != 2 {
		t.Fatalf("rounds by close year: %+v", ys)
	}
	// 基准同样跨年衔接：2026 的起点是 2025 最后一根基准 1010 而不是初始 1000。
	if !near(ys[0].BenchmarkReturn, 0.01) || !near(ys[1].BenchmarkReturn, 1060.5/1010-1) {
		t.Fatalf("benchmark carry: %+v", ys)
	}
}

func TestYearlyBenchmarkGap(t *testing.T) {
	// 某年最后一根基准为 0：该年基准收益静默为 0，且不把起点顺延给下一年
	// （startBench 只在取到正基准时推进，metrics.go :188-191）。
	eq := []EquityPoint{
		{Day: day("2025-12-31"), Equity: 1050, Benchmark: 1010},
		{Day: day("2026-01-05"), Equity: 1100, Benchmark: 0},
		{Day: day("2027-01-04"), Equity: 1200, Benchmark: 1111},
	}
	ys := Yearly(1000, eq, nil)
	if len(ys) != 3 {
		t.Fatalf("years: %+v", ys)
	}
	if ys[1].BenchmarkReturn != 0 {
		t.Fatalf("gap year benchmark must stay 0: %+v", ys[1])
	}
	// 2027 的起点仍是 2025 推进下来的 1010：1111/1010 − 1 = 10%。
	if !near(ys[2].BenchmarkReturn, 0.1) {
		t.Fatalf("benchmark start must survive the gap year: %+v", ys[2])
	}
}

func TestComputeBenchmarkAndExcess(t *testing.T) {
	// 设计 :194 超额 = 累计 − 基准累计；基准按末日读数 / 初始资金 − 1（初始资金处归一）。
	eq := []EquityPoint{
		{Day: day("2026-01-05"), Equity: 110, Benchmark: 105},
		{Day: day("2026-01-06"), Equity: 120, Benchmark: 112},
	}
	m := Compute(100, 0, eq, nil, nil)
	if !near(m.BenchmarkReturn, 0.12) || !near(m.Excess, 0.20-0.12) {
		t.Fatalf("bench/excess: %+v", m)
	}

	// 末日基准为 0（哪怕中间有点）：基准与超额都静默不落，不拿中间值凑数（metrics.go :107）。
	eq2 := []EquityPoint{
		{Day: day("2026-01-05"), Equity: 110, Benchmark: 105},
		{Day: day("2026-01-06"), Equity: 120, Benchmark: 0},
	}
	m2 := Compute(100, 0, eq2, nil, nil)
	if m2.BenchmarkReturn != 0 || m2.Excess != 0 {
		t.Fatalf("zero last benchmark must stay silent: %+v", m2)
	}
}

func TestComputeZeroRoundCountsAsNotWin(t *testing.T) {
	// 设计 :188 盈亏为 0 算不盈利：回合进分母、不进胜场，也不进盈亏比的两侧。
	rounds := []Round{
		{PnL: 0, Return: 0, HoldDays: 2},
		{PnL: 10, Return: 0.1, HoldDays: 4},
		{PnL: -6, Return: -0.06, HoldDays: 6},
	}
	m := Compute(100, 0, []EquityPoint{{Day: day("2026-01-05"), Equity: 104}}, rounds, nil)
	if !near(m.WinRate, 1.0/3) {
		t.Fatalf("zero round must count in the denominator as not-win: %v", m.WinRate)
	}
	if !near(m.Expectancy, 0.04/3) {
		t.Fatalf("expectancy = %v", m.Expectancy)
	}
	// 设计 :192 平均持仓天数 = 各回合 HoldDays 的均值。
	if !near(m.AvgHoldDays, 4) {
		t.Fatalf("avg hold = %v", m.AvgHoldDays)
	}
	if !near(m.AvgWin, 10) || !near(m.AvgLoss, 6) || !near(m.ProfitFactor, 10.0/6) {
		t.Fatalf("win/loss stats: %+v", m)
	}
}

func TestComputeAllWinnersProfitFactorZero(t *testing.T) {
	// 全赢无亏：AvgLoss 为 0，盈亏比静默为 0 而不是 Inf（metrics.go :91-93 的口径选择）。
	rounds := []Round{{PnL: 10, Return: 0.1}, {PnL: 30, Return: 0.3}}
	m := Compute(100, 0, []EquityPoint{{Day: day("2026-01-05"), Equity: 140}}, rounds, nil)
	if !near(m.AvgWin, 20) || m.AvgLoss != 0 || m.ProfitFactor != 0 {
		t.Fatalf("all winners: %+v", m)
	}
	if m.WinRate != 1 {
		t.Fatalf("win rate = %v", m.WinRate)
	}
}

func TestComputeSharpeSampleStdAndRiskFree(t *testing.T) {
	// 设计 :186 夏普 =（日收益均值 − rf/252）÷ 日收益样本标准差 × √252。
	// 期望全部手算成常数（不借被测的 meanStd）：日收益 {0.1, −0.1, 1000/990−1}，
	// 样本标准差按 n−1=2 摊（metrics.go :140 的 sd/(len−1)，改成 /n 这里必红）。
	eq := []EquityPoint{
		{Day: day("2026-03-02"), Equity: 1100},
		{Day: day("2026-03-03"), Equity: 990},
		{Day: day("2026-03-04"), Equity: 1000},
	}
	r0, r1, r2 := 0.1, -0.1, 1000.0/990.0-1
	mean := (r0 + r1 + r2) / 3
	sd := math.Sqrt((math.Pow(r0-mean, 2) + math.Pow(r1-mean, 2) + math.Pow(r2-mean, 2)) / 2)
	m0 := Compute(1000, 0, eq, nil, nil)
	if !near(m0.Sharpe, mean/sd*math.Sqrt(252)) {
		t.Fatalf("sharpe rf=0 = %v, want %v", m0.Sharpe, mean/sd*math.Sqrt(252))
	}
	// rf 项的方向（减号）与量纲（/252）：rf=2% 时夏普恰降 rf/252/sd×√252，全库此前 rf 恒 0 裸奔。
	const rf = 0.02
	mRf := Compute(1000, rf, eq, nil, nil)
	if want := (mean - rf/252) / sd * math.Sqrt(252); !near(mRf.Sharpe, want) {
		t.Fatalf("sharpe rf=.02 = %v, want %v", mRf.Sharpe, want)
	}
	if !near(m0.Sharpe-mRf.Sharpe, (rf/252)/sd*math.Sqrt(252)) {
		t.Fatalf("rf delta = %v", m0.Sharpe-mRf.Sharpe)
	}
}

func TestWorstStableOrderAndFilter(t *testing.T) {
	// Worst 只收亏损回合（盈亏为 0 不算），按亏损从大到小，平局保持原顺序（SliceStable），
	// 报告端按 n=5 截断取最差 5 回合（BuildReport :264）。
	rounds := []Round{
		{ID: 1, PnL: -5},
		{ID: 2, PnL: 10},
		{ID: 3, PnL: -30},
		{ID: 4, PnL: 0},
		{ID: 5, PnL: -30},
		{ID: 6, PnL: -1},
	}
	w := Worst(rounds, 3)
	if len(w) != 3 || w[0].ID != 3 || w[1].ID != 5 || w[2].ID != 1 {
		t.Fatalf("worst order: %+v", w)
	}
	// n 超过亏损回合数时全量返回，不补零/正回合。
	w2 := Worst(rounds, 10)
	if len(w2) != 4 || w2[3].ID != 6 {
		t.Fatalf("worst all: %+v", w2)
	}
	// 没有亏损回合 → 空。
	if w3 := Worst([]Round{{ID: 1, PnL: 1}, {ID: 2, PnL: 0}}, 5); len(w3) != 0 {
		t.Fatalf("no losers: %+v", w3)
	}
}
