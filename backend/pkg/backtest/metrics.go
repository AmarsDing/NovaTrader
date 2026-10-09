package backtest

import (
	"math"
	"sort"
	"time"
)

const tradingDaysPerYear = 252

// Metrics 是设计文档第 6 节的指标。比例都是小数，0.1 表示 10%。
type Metrics struct {
	TotalReturn     float64   `json:"total_return"`
	AnnualReturn    float64   `json:"annual_return"`
	MaxDrawdown     float64   `json:"max_drawdown"`
	DrawdownPeak    time.Time `json:"drawdown_peak"`
	DrawdownTrough  time.Time `json:"drawdown_trough"`
	Sharpe          float64   `json:"sharpe"`
	Calmar          float64   `json:"calmar"`
	WinRate         float64   `json:"win_rate"`
	ProfitFactor    float64   `json:"profit_factor"`
	Expectancy      float64   `json:"expectancy"`
	AvgWin          float64   `json:"avg_win"`
	AvgLoss         float64   `json:"avg_loss"`
	Turnover        float64   `json:"turnover"`
	AvgHoldDays     float64   `json:"avg_hold_days"`
	Rounds          int       `json:"rounds"`
	Fills           int       `json:"fills"`
	Days            int       `json:"days"`
	FinalEquity     float64   `json:"final_equity"`
	BenchmarkReturn float64   `json:"benchmark_return"`
	Excess          float64   `json:"excess"`
}

// YearStat 是一个自然年的表现。
type YearStat struct {
	Year            int     `json:"year"`
	Return          float64 `json:"return"`
	MaxDrawdown     float64 `json:"max_drawdown"`
	Rounds          int     `json:"rounds"`
	BenchmarkReturn float64 `json:"benchmark_return"`
}

// Compute 按初始资金、日权益、回合和成交算指标。权益序列为空时返回零值。
func Compute(initial, riskFree float64, equity []EquityPoint, rounds []Round, trades []Trade) Metrics {
	m := Metrics{Rounds: len(rounds), Days: len(equity)}
	for _, t := range trades {
		if t.Side == "buy" || t.Side == "sell" {
			m.Fills++
		}
	}
	if len(equity) == 0 || initial <= 0 {
		return m
	}
	last := equity[len(equity)-1]
	m.FinalEquity = last.Equity
	m.TotalReturn = last.Equity/initial - 1
	n := float64(len(equity))
	if m.TotalReturn > -1 {
		m.AnnualReturn = math.Pow(1+m.TotalReturn, tradingDaysPerYear/n) - 1
	} else {
		m.AnnualReturn = -1
	}
	m.MaxDrawdown, m.DrawdownPeak, m.DrawdownTrough = maxDrawdown(initial, equity)
	rets := dailyReturns(initial, equity)
	mean, sd := meanStd(rets)
	if sd > 0 {
		m.Sharpe = (mean - riskFree/tradingDaysPerYear) / sd * math.Sqrt(tradingDaysPerYear)
	}
	if m.MaxDrawdown > 0 {
		m.Calmar = m.AnnualReturn / m.MaxDrawdown
	}
	var wins, losses []float64
	sumRet, sumHold := 0.0, 0
	for _, r := range rounds {
		if r.PnL > 0 {
			wins = append(wins, r.PnL)
		} else if r.PnL < 0 {
			losses = append(losses, -r.PnL)
		}
		sumRet += r.Return
		sumHold += r.HoldDays
	}
	if len(rounds) > 0 {
		m.WinRate = float64(len(wins)) / float64(len(rounds))
		m.Expectancy = sumRet / float64(len(rounds))
		m.AvgHoldDays = float64(sumHold) / float64(len(rounds))
	}
	m.AvgWin, _ = meanStd(wins)
	m.AvgLoss, _ = meanStd(losses)
	if m.AvgLoss > 0 {
		m.ProfitFactor = m.AvgWin / m.AvgLoss
	}
	traded, avgEq := 0.0, 0.0
	for _, t := range trades {
		if t.Side == "buy" || t.Side == "sell" {
			traded += t.Amount
		}
	}
	for _, p := range equity {
		avgEq += p.Equity
	}
	avgEq /= n
	if avgEq > 0 {
		m.Turnover = traded / 2 / avgEq * tradingDaysPerYear / n
	}
	if last.Benchmark > 0 {
		m.BenchmarkReturn = last.Benchmark/initial - 1
		m.Excess = m.TotalReturn - m.BenchmarkReturn
	}
	return m
}

func dailyReturns(initial float64, equity []EquityPoint) []float64 {
	out := make([]float64, 0, len(equity))
	prev := initial
	for _, p := range equity {
		if prev > 0 {
			out = append(out, p.Equity/prev-1)
		}
		prev = p.Equity
	}
	return out
}

func meanStd(v []float64) (mean, sd float64) {
	if len(v) == 0 {
		return 0, 0
	}
	for _, x := range v {
		mean += x
	}
	mean /= float64(len(v))
	if len(v) < 2 {
		return mean, 0
	}
	for _, x := range v {
		sd += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(sd / float64(len(v)-1))
}

func maxDrawdown(initial float64, equity []EquityPoint) (dd float64, peakDay, troughDay time.Time) {
	peak := initial
	var curPeakDay time.Time
	for _, p := range equity {
		if p.Equity > peak {
			peak, curPeakDay = p.Equity, p.Day
		}
		if peak > 0 {
			if d := (peak - p.Equity) / peak; d > dd {
				dd, peakDay, troughDay = d, curPeakDay, p.Day
			}
		}
	}
	return dd, peakDay, troughDay
}

// FillDrawdown 写入每个权益点的回撤。
func FillDrawdown(initial float64, equity []EquityPoint) {
	peak := initial
	for i := range equity {
		if equity[i].Equity > peak {
			peak = equity[i].Equity
		}
		if peak > 0 {
			equity[i].Drawdown = (peak - equity[i].Equity) / peak
		}
	}
}

// Yearly 按自然年切分。每年的起点是上一年最后一个权益（首年为初始资金）。
func Yearly(initial float64, equity []EquityPoint, rounds []Round) []YearStat {
	var out []YearStat
	startEq, startBench := initial, initial
	for i := 0; i < len(equity); {
		year := equity[i].Day.Year()
		j := i
		for j < len(equity) && equity[j].Day.Year() == year {
			j++
		}
		seg := equity[i:j]
		ys := YearStat{Year: year}
		if startEq > 0 {
			ys.Return = seg[len(seg)-1].Equity/startEq - 1
		}
		ys.MaxDrawdown, _, _ = maxDrawdown(startEq, seg)
		if b := seg[len(seg)-1].Benchmark; b > 0 && startBench > 0 {
			ys.BenchmarkReturn = b/startBench - 1
			startBench = b
		}
		for _, r := range rounds {
			if r.CloseTime.Year() == year {
				ys.Rounds++
			}
		}
		out = append(out, ys)
		startEq = seg[len(seg)-1].Equity
		i = j
	}
	return out
}

// Worst 返回亏损最大的 n 个回合。
func Worst(rounds []Round, n int) []Round {
	out := make([]Round, 0, len(rounds))
	for _, r := range rounds {
		if r.PnL < 0 {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PnL < out[j].PnL })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Report 是一个任务的完整报告，整体存进 backtest_report.result。
type Report struct {
	Kind        string             `json:"kind"`
	Config      Config             `json:"config"`
	Metrics     Metrics            `json:"metrics"`
	Yearly      []YearStat         `json:"yearly"`
	Equity      []EquityPoint      `json:"equity"`
	Worst       []Round            `json:"worst"`
	Rounds      []Round            `json:"rounds"`
	Open        []OpenPosition     `json:"open"`
	Rejects     int                `json:"rejects"`
	RejectWhy   map[string]int     `json:"reject_why"`
	Warnings    []string           `json:"warnings"`
	Assumptions []string           `json:"assumptions"`
	DataHash    string             `json:"data_hash"`
	FillsHash   string             `json:"fills_hash"`
	DataCutoff  time.Time          `json:"data_cutoff"`
	Lookahead   *LookaheadResult   `json:"lookahead,omitempty"`
	MonteCarlo  *MonteCarloResult  `json:"monte_carlo,omitempty"`
	Optimize    *OptimizeResult    `json:"optimize,omitempty"`
	WalkForward *WalkForwardResult `json:"walk_forward,omitempty"`
	Evolve      *EvolveResult      `json:"evolve,omitempty"`
	Reproduce   *Reproduce         `json:"reproduce,omitempty"`
}

// Reproduce 是重跑任务与原任务的比对结果。
type Reproduce struct {
	OriginTaskID int    `json:"origin_task_id"`
	SameData     bool   `json:"same_data"`
	SameFills    bool   `json:"same_fills"`
	Verdict      string `json:"verdict"`
}

// BuildReport 由一次运行的结果生成报告。
func BuildReport(kind string, res *Result) *Report {
	FillDrawdown(res.Config.InitialCash, res.Equity)
	return &Report{
		Kind:        kind,
		Config:      res.Config,
		Metrics:     Compute(res.Config.InitialCash, res.Config.RiskFree, res.Equity, res.Rounds, res.Trades),
		Yearly:      Yearly(res.Config.InitialCash, res.Equity, res.Rounds),
		Equity:      res.Equity,
		Worst:       Worst(res.Rounds, 5),
		Rounds:      res.Rounds,
		Open:        res.Open,
		Rejects:     res.Rejects,
		RejectWhy:   res.RejectWhy,
		Warnings:    res.Warnings,
		Assumptions: Assumptions(res.Config),
		DataHash:    res.DataHash,
		FillsHash:   res.FillsHash,
		DataCutoff:  res.DataCutoff,
	}
}

// Assumptions 是写进报告的成交口径，换口径必须换任务参数。
func Assumptions(c Config) []string {
	out := []string{
		"信号在 K 线走完后产生，用下一根 K 线开盘价成交，每张委托只撮合一次",
		"单笔成交不超过该 K 线成交量的 " + pct(c.VolumeCap),
		"买入加 " + itoa(c.SlippageTicks) + " 个最小价位滑点，卖出减同样价位",
		"佣金 " + bp(c.Fee.CommissionRate) + "、最低 " + ftoa(c.Fee.MinCommission) + " 元，卖出印花税 " + bp(c.Fee.StampTaxRate) + "，过户费 " + bp(c.Fee.TransferRate),
		"T+1：当日买入下一交易日才可卖；不隔夜挂单",
		"历史日线按决策日复权；持仓跨除权日按现金等值入账（近似）",
	}
	if c.LimitFill == "never" {
		out = append(out, "成交价落在涨停（跌停）价就不成交（保守口径）")
	} else {
		out = append(out, "K 线最低价低于涨停价即认为打开过、能买到；分钟线看不到排队，打板成交率可能偏高")
	}
	if c.Freq == Freq1d {
		out = append(out, "日线回测：15:00 出信号，下一交易日开盘成交，只做粗筛")
	}
	return out
}
