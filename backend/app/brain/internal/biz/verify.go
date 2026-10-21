// verify.go：明日验证对账（P0-2）。信号日（上一交易日）晨报 watchlist 候选，
// 对账日收盘后回填实际表现：次日涨幅、是否再涨停、命中率。
// 纪律：涨幅没取到用 NULL，不并入分母，不落 0；missing 写明没取到的块。
package biz

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// CheckCandidate 对账名单里的一只票。
type CheckCandidate struct {
	Symbol string `json:"symbol"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason,omitempty"`
	Text   string `json:"text,omitempty"` // 入选逻辑摘录
	// SignalID 仅 source=signal 使用（M13-T08），briefing 源不输出。
	SignalID int64 `json:"signal_id,omitempty"`
}

// CheckResult 一只票的次日表现。Pct 为 nil 表示日线没取到。
type CheckResult struct {
	Symbol  string   `json:"symbol"`
	Name    string   `json:"name,omitempty"`
	Pct     *float64 `json:"pct,omitempty"`
	Relimit bool     `json:"relimit"`
	Hit     bool     `json:"hit"` // pct > 0 / source=signal 时为「入场成交且止盈先于止损」
	// 以下仅 source=signal 使用（M13-T08），omitempty 保证 briefing 源 JSON 不变，前端兼容。
	SignalID  *int64  `json:"signal_id,omitempty"`
	Horizon   *int    `json:"horizon,omitempty"`    // T+N 的 N（1/3/5）
	TradeDate *string `json:"trade_date,omitempty"` // 信号交易日 2006-01-02
	Filled    *bool   `json:"filled,omitempty"`     // 窗口内是否触及入场带
	StopHit   *bool   `json:"stop_hit,omitempty"`
	TpHit     *bool   `json:"tp_hit,omitempty"`
}

// Check 一日对账行。candidate/hit 等标量为 nil 表示没取到。
type Check struct {
	SignalDate     time.Time
	CheckDate      time.Time
	Source         string
	Candidates     []CheckCandidate
	Results        []CheckResult
	CandidateCount *int
	HitCount       *int
	RelimitCount   *int
	HitRate        *float64
	AvgPct         *float64
	MaxPct         *float64
	Partial        bool
	Missing        []string
}

// VerifyPredictions 跑一日明日验证：默认 source=briefing 晨报 watchlist。
// 幂等，同一天可重跑（Upsert 覆盖）。
func (uc *Usecase) VerifyPredictions(ctx context.Context, checkDay time.Time) (*Check, error) {
	checkDay = shDate(checkDay)
	signalDay, err := uc.cal.PrevOpen(checkDay)
	if err != nil {
		return nil, err
	}
	c := &Check{SignalDate: signalDay, CheckDate: checkDay, Source: "briefing"}

	b, err := uc.briefings.Get(ctx, signalDay)
	if err != nil {
		return nil, err
	}
	if b == nil || len(b.Watchlist) == 0 {
		c.Partial = true
		c.Missing = append(c.Missing, "briefing")
		return c, uc.reviews.UpsertPredictionCheck(ctx, c)
	}
	for _, w := range b.Watchlist {
		cd := CheckCandidate{Symbol: w.Symbol, Reason: w.Reason, Text: w.Reason}
		if len(w.Evidence) > 0 {
			cd.Text = w.Evidence[0]
		}
		c.Candidates = append(c.Candidates, cd)
	}
	c.CandidateCount = intPtr(len(c.Candidates))

	symbols := make([]string, 0, len(c.Candidates))
	for _, cd := range c.Candidates {
		symbols = append(symbols, cd.Symbol)
	}
	pool, err := uc.reviews.PoolOn(ctx, checkDay)
	if err != nil {
		return nil, err
	}
	upSet := map[string]bool{}
	if len(pool) == 0 {
		c.Partial = true
		c.Missing = append(c.Missing, "limit_pool")
	}
	for _, r := range pool {
		if r.Pool == "up" {
			upSet[r.Symbol] = true
		}
	}
	bars, err := uc.reviews.BarOn(ctx, symbols, checkDay)
	if err != nil {
		return nil, err
	}

	hits, relimits, sum, valid := 0, 0, 0.0, 0
	maxPct := 0.0
	for _, cd := range c.Candidates {
		res := CheckResult{Symbol: cd.Symbol, Name: cd.Name, Relimit: upSet[cd.Symbol]}
		if b, ok := bars[cd.Symbol]; ok && b.Close > 0 && b.PreClose > 0 {
			pct := r2((b.Close - b.PreClose) / b.PreClose * 100)
			res.Pct = &pct
			res.Hit = pct > 0
			if res.Hit {
				hits++
			}
			if res.Relimit {
				relimits++
			}
			sum += pct
			valid++
			if pct > maxPct {
				maxPct = pct
			}
		} else {
			c.Partial = true
			missingList := "bars"
			if !contains(c.Missing, missingList) {
				c.Missing = append(c.Missing, missingList)
			}
		}
		c.Results = append(c.Results, res)
	}
	if valid > 0 {
		c.HitCount = intPtr(hits)
		c.RelimitCount = intPtr(relimits)
		c.HitRate = f64Ptr(r2(float64(hits) / float64(valid) * 100))
		c.AvgPct = f64Ptr(r2(sum / float64(valid)))
		c.MaxPct = f64Ptr(maxPct)
	}
	sort.Strings(c.Missing)
	return c, uc.reviews.UpsertPredictionCheck(ctx, c)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func boolPtr(v bool) *bool { return &v }

// ---------------------------------------------------------------------------
// M13-T08：source=signal 的信号级明日验证。15:45 任务对 status∈(done,closed) 且
// T+1/T+3/T+5 到期的买入信号，逐条按入场带/止损/止盈对信号日后 N 根日线检验。
// 纪律：缺行情（窗口内日线不足 N 根）一律 result=partial，Pct 不进分母，missing 记 bars；
// 幂等 upsert（(check_date, source) 冲突覆盖，同 briefing 源）。
// ---------------------------------------------------------------------------

// SignalRow 是 source=signal 对账读到的终态买入信号。
type SignalRow struct {
	ID         int64
	StockCode  string
	StockName  string
	Strategy   string
	SignalTime time.Time
	TradeDate  *time.Time // 为 nil 时按 SignalTime 的上海日期回退（显式回退，不猜 0）
	EntryPrice float64
	StopLoss   float64
	TakeProfit float64
	EntryLow   *float64
	EntryHigh  *float64
	Status     string
}

// VerifySignals 跑一日 source=signal 对账。当天没有到期终态信号属正常态：返回空 Check，不落对账行。
func (uc *Usecase) VerifySignals(ctx context.Context, checkDay time.Time) (*Check, error) {
	checkDay = shDate(checkDay)
	// horizon 队列：checkDay 向前 1/3/5 个交易日到期。
	day, err := uc.cal.PrevOpen(checkDay)
	if err != nil {
		return nil, err
	}
	var days []time.Time
	horizonOf := map[string]int{}
	for _, n := range []int{1, 3, 5} {
		for len(days) < n {
			days = append(days, day)
			if day, err = uc.cal.PrevOpen(day); err != nil {
				return nil, err
			}
		}
		horizonOf[days[n-1].Format("2006-01-02")] = n
	}
	matured := []time.Time{days[0], days[2], days[4]}
	signals, err := uc.reviews.SignalsOnDays(ctx, matured)
	if err != nil {
		return nil, err
	}
	c := &Check{SignalDate: days[0], CheckDate: checkDay, Source: "signal"}
	if len(signals) == 0 {
		return c, nil
	}
	c.CandidateCount = intPtr(len(signals))

	hits, sum, valid := 0, 0.0, 0
	maxPct := 0.0
	for _, s := range signals {
		sday := signalTradeDay(s)
		n, ok := horizonOf[sday.Format("2006-01-02")]
		if !ok {
			continue // repo 已按到期日过滤；防御性跳过
		}
		lo, hi := entryBand(s)
		cd := CheckCandidate{Symbol: s.StockCode, Name: s.StockName, Reason: s.Strategy, SignalID: s.ID,
			Text: entryText(lo, hi, s.StopLoss, s.TakeProfit)}
		c.Candidates = append(c.Candidates, cd)

		sid, hz, td := s.ID, n, sday.Format("2006-01-02")
		res := CheckResult{Symbol: s.StockCode, Name: s.StockName, SignalID: &sid, Horizon: &hz, TradeDate: &td}
		window, werr := uc.signalWindow(ctx, s.StockCode, sday, checkDay, n)
		if werr != nil {
			return nil, werr
		}
		filled, stopIdx, tpIdx := evalWindow(window, lo, hi, s.StopLoss, s.TakeProfit)
		res.Filled, res.StopHit, res.TpHit = &filled, boolPtr(stopIdx >= 0), boolPtr(tpIdx >= 0)
		switch {
		case len(window) < n:
			// 「没取到≠0」：T+N 行情不齐，Pct 不落不进分母，只标 partial。
			c.Partial = true
			if !contains(c.Missing, "bars") {
				c.Missing = append(c.Missing, "bars")
			}
		case s.EntryPrice <= 0:
			c.Partial = true
			if !contains(c.Missing, "entry_price") {
				c.Missing = append(c.Missing, "entry_price")
			}
		default:
			pct := r2((window[len(window)-1].Close - s.EntryPrice) / s.EntryPrice * 100)
			res.Pct = &pct
			// 命中 = 触及入场带 且 止盈先于止损（同根 bar 双触发按止损优先，从保守）。
			res.Hit = filled && tpIdx >= 0 && (stopIdx < 0 || tpIdx < stopIdx)
			if res.Hit {
				hits++
			}
			sum += pct
			valid++
			if pct > maxPct {
				maxPct = pct
			}
		}
		c.Results = append(c.Results, res)
	}
	if valid > 0 {
		c.HitCount = intPtr(hits)
		c.HitRate = f64Ptr(r2(float64(hits) / float64(valid) * 100))
		c.AvgPct = f64Ptr(r2(sum / float64(valid)))
		c.MaxPct = f64Ptr(maxPct)
	}
	// RelimitCount 对信号源不适用，保持 nil（显式缺失，不落 0）。
	sort.Strings(c.Missing)
	return c, uc.reviews.UpsertPredictionCheck(ctx, c)
}

// signalTradeDay 信号的交易日：trade_date 优先，NULL 时回退 signal_time 的上海日期。
func signalTradeDay(s SignalRow) time.Time {
	if s.TradeDate != nil {
		return shDate(*s.TradeDate)
	}
	return shDate(s.SignalTime)
}

// entryBand 入场带：低端/高端未设（NULL 或 0）时回落到 entry_price 单点。
func entryBand(s SignalRow) (lo, hi float64) {
	lo, hi = s.EntryPrice, s.EntryPrice
	if s.EntryLow != nil && *s.EntryLow > 0 {
		lo = *s.EntryLow
	}
	if s.EntryHigh != nil && *s.EntryHigh > 0 {
		hi = *s.EntryHigh
	}
	return lo, hi
}

// entryText 入场/止损/止盈的逻辑摘录；价格没设的档标「未设」，不落 0。
func entryText(lo, hi, stop, tp float64) string {
	entry := "入场带未设"
	if lo > 0 && hi > 0 {
		entry = fmt.Sprintf("入场 %.2f～%.2f", lo, hi)
	}
	stopText, tpText := "止损未设", "止盈未设"
	if stop > 0 {
		stopText = fmt.Sprintf("止损 %.2f", stop)
	}
	if tp > 0 {
		tpText = fmt.Sprintf("止盈 %.2f", tp)
	}
	return entry + " " + stopText + " " + tpText
}

// signalWindow 取信号日之后 n 根日线（至 checkDay 止，不含信号日）。
// 不足 n 根时返回已有部分，调用方按 partial 处理（停牌/采集未到都算缺行情）。
func (uc *Usecase) signalWindow(ctx context.Context, sym string, signalDay, checkDay time.Time, n int) ([]Bar, error) {
	bars, err := uc.market.DailyBars(ctx, sym, checkDay, n+1)
	if err != nil {
		return nil, err
	}
	out := make([]Bar, 0, n)
	for _, b := range bars {
		t := shDate(b.Time)
		if t.After(signalDay) && !t.After(checkDay) {
			out = append(out, b)
		}
	}
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// evalWindow 在 T+N 窗口内判定：入场带是否触及、首个止损 bar、首个止盈 bar（-1 表示未触发）。
func evalWindow(bars []Bar, lo, hi, stop, tp float64) (filled bool, stopIdx, tpIdx int) {
	stopIdx, tpIdx = -1, -1
	for i, b := range bars {
		if lo > 0 && hi > 0 && b.Low <= hi && b.High >= lo {
			filled = true
		}
		if stop > 0 && stopIdx < 0 && b.Low <= stop {
			stopIdx = i
		}
		if tp > 0 && tpIdx < 0 && b.High >= tp {
			tpIdx = i
		}
	}
	return filled, stopIdx, tpIdx
}
