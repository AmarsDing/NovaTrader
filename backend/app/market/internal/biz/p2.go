package biz

import (
	"context"
	"math"
	"sort"
	"time"

	"server/pkg/market"
)

// P2（监控驾驶舱业务监控方案 §7）：K 线连续性检查、昨日涨停今日溢价跨日快照、20 日均线上占比。
// 原则与 P1 相同：只读聚合，缺数据就 found=false，不拿别的口径冒充，不编 0。

/* ---------- K 线连续性检查 ---------- */

// DailyBarDay 是一根 1d 收盘行的存在性记录（symbol + 所属交易日）。
type DailyBarDay struct {
	Symbol string
	Day    time.Time
}

// KlineGapDay 是一个交易日的日线连续性结果。Missing=0 表示该日无缺口。
type KlineGapDay struct {
	Day      time.Time
	Universe int
	Present  int
	Missing  int
	Sample   []string
	AsOf     time.Time
}

// KlineGapReport 是窗口内的缺口报告。Found 为真表示至少有一天有缺口。
type KlineGapReport struct {
	Day          time.Time
	Found        bool
	Universe     int
	Days         []KlineGapDay
	MissingTotal int
	AsOf         time.Time
}

const gapSampleCap = 20

// KlineGaps 检查 [day-days, day] 窗口内每个交易日的日线连续性。
// 股票池 = 窗口内出现过 1d 收盘行的股票（停牌/退市/未采集的不在池内，不计缺口）；
// 某天缺口 = 池内没有该日收盘行的股票。停牌按缺口计——这正是体检要看见的。
func (u *Usecase) KlineGaps(ctx context.Context, day time.Time, days int) (*KlineGapReport, error) {
	if days <= 0 {
		days = 5
	}
	if days > 20 {
		days = 20
	}
	window := u.openDays(ctx, day, days)
	report := &KlineGapReport{Day: day}
	if len(window) == 0 {
		return report, nil
	}
	from := window[len(window)-1]
	rows, err := u.repo.DailyBarDays(ctx, from, day.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	present := make(map[string]map[string]struct{}, len(window))
	for _, d := range window {
		present[keyDay(d)] = map[string]struct{}{}
	}
	universe := map[string]struct{}{}
	for _, r := range rows {
		k := keyDay(r.Day)
		if set, ok := present[k]; ok {
			set[r.Symbol] = struct{}{}
		}
		universe[r.Symbol] = struct{}{}
	}
	report.Universe = len(universe)
	now := u.now()
	for _, d := range window {
		set := present[keyDay(d)]
		missing := make([]string, 0, len(universe))
		for sym := range universe {
			if _, ok := set[sym]; !ok {
				missing = append(missing, sym)
			}
		}
		sort.Strings(missing)
		gap := KlineGapDay{
			Day:      d,
			Universe: len(universe),
			Present:  len(set),
			Missing:  len(missing),
			AsOf:     now,
		}
		if len(missing) > 0 {
			report.Found = true
			report.MissingTotal += len(missing)
			if len(missing) > gapSampleCap {
				missing = missing[:gapSampleCap]
			}
			gap.Sample = missing
		}
		report.Days = append(report.Days, gap)
	}
	report.AsOf = now
	return report, nil
}

// QualityIssue 是一条要落 data_quality_issue 的质量问题（symbol 为空表示整域）。
type QualityIssue struct {
	Domain    string
	Check     string
	Symbol    string
	TradeDate time.Time
	Severity  string
	Source    string
	Message   string
	Detail    map[string]any
}

// KlineCheck 是收盘后的日线连续性体检任务：把当日缺口写进 data_quality_issue。
// 缺口率 >5% 记 error，否则 warn；整域一行汇总 + 逐票一行（最多 500 行，其余只进汇总）。
func (u *Usecase) KlineCheck(ctx context.Context, now time.Time) error {
	day := market.DateOf(now)
	open, err := u.cal.Open(day)
	if err != nil {
		return err
	}
	if !open {
		return nil
	}
	rows, err := u.repo.DailyBarDays(ctx, day.AddDate(0, 0, -30), day.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	universe := map[string]struct{}{}
	today := map[string]struct{}{}
	todayKey := keyDay(day)
	for _, r := range rows {
		universe[r.Symbol] = struct{}{}
		if keyDay(r.Day) == todayKey {
			today[r.Symbol] = struct{}{}
		}
	}
	missing := make([]string, 0, 64)
	for sym := range universe {
		if _, ok := today[sym]; !ok {
			missing = append(missing, sym)
		}
	}
	sort.Strings(missing)
	if len(universe) == 0 || len(missing) == 0 {
		return nil
	}
	rate := float64(len(missing)) / float64(len(universe))
	severity := "warn"
	if rate > 0.05 {
		severity = "error"
	}
	detail := map[string]any{
		"universe": len(universe),
		"missing":  len(missing),
		"rate":     rate,
	}
	sample := missing
	if len(sample) > gapSampleCap {
		sample = sample[:gapSampleCap]
	}
	detail["sample"] = sample
	issues := make([]QualityIssue, 0, len(sample)+1)
	issues = append(issues, QualityIssue{
		Domain: "market", Check: "completeness", Symbol: "", TradeDate: day,
		Severity: severity, Source: "market.kline-check",
		Message: "日线缺口：全市场 " + itoa(len(missing)) + "/" + itoa(len(universe)) + " 只缺 1d 收盘行",
		Detail:  detail,
	})
	const perSymbolCap = 500
	for i, sym := range missing {
		if i >= perSymbolCap {
			break
		}
		issues = append(issues, QualityIssue{
			Domain: "market", Check: "completeness", Symbol: sym, TradeDate: day,
			Severity: "warn", Source: "market.kline-check",
			Message: "该股票 " + day.Format("2006-01-02") + " 无日线收盘行（停牌或采集缺失）",
			Detail:  map[string]any{"universe": len(universe), "missing": len(missing)},
		})
	}
	return u.repo.SaveIssues(ctx, issues)
}

/* ---------- 昨日涨停今日溢价 ---------- */

// PremiumRow 是 limit_premium 的一行：一只股票一个溢价日。
type PremiumRow struct {
	Symbol       string
	Name         string
	TradeDate    time.Time
	BaseDate     time.Time
	Consecutive  int
	BaseClose    float64
	Open         float64
	High         float64
	Close        float64
	OpenPremium  float64
	ClosePremium float64
	HighPremium  float64
	LimitAgain   bool
	HasClose     bool
	Source       string
	AsOf         time.Time
}

// PremiumDay 是一个溢价日的汇总。
type PremiumDay struct {
	TradeDate  time.Time
	BaseDate   time.Time
	Sample     int
	LimitAgain int
	OpenAvg    float64
	CloseAvg   float64
	CloseWin   float64
	HighAvg    float64
	HasClose   bool
}

// PremiumReport 是溢价快照读结果。Live 为真表示快照表没落库、按 market_data 实时兜底。
type PremiumReport struct {
	Day     time.Time
	Found   bool
	Live    bool
	Summary PremiumDay
	Days    []PremiumDay
	Rows    []PremiumRow
	AsOf    time.Time
}

// premiumRow 把一行溢价记录算齐：开盘/收盘/最高溢价，分母 base_close。
func premiumRow(rec PremiumRow) PremiumRow {
	rec.HasClose = rec.Close > 0 && rec.BaseClose > 0
	if rec.BaseClose > 0 {
		if rec.Open > 0 {
			rec.OpenPremium = rec.Open/rec.BaseClose - 1
		}
		if rec.Close > 0 {
			rec.ClosePremium = rec.Close/rec.BaseClose - 1
		}
		if rec.High > 0 {
			rec.HighPremium = rec.High/rec.BaseClose - 1
		}
	}
	for _, p := range []*float64{&rec.OpenPremium, &rec.ClosePremium, &rec.HighPremium} {
		if math.IsNaN(*p) || math.IsInf(*p, 0) {
			*p = 0
		}
	}
	return rec
}

// summarizePremium 把一组同日的行汇总成 PremiumDay。
func summarizePremium(rows []PremiumRow) PremiumDay {
	day := PremiumDay{}
	if len(rows) == 0 {
		return day
	}
	day.TradeDate = rows[0].TradeDate
	day.BaseDate = rows[0].BaseDate
	var openSum, closeSum, highSum float64
	var openN, closeN, highN, winN int
	for _, r := range rows {
		day.Sample++
		if r.LimitAgain {
			day.LimitAgain++
		}
		if r.Open > 0 && r.BaseClose > 0 {
			openSum += r.OpenPremium
			openN++
		}
		if r.HasClose {
			closeSum += r.ClosePremium
			closeN++
			day.HasClose = true
			if r.ClosePremium > 0 {
				winN++
			}
		}
		if r.High > 0 && r.BaseClose > 0 {
			highSum += r.HighPremium
			highN++
		}
	}
	if openN > 0 {
		day.OpenAvg = openSum / float64(openN)
	}
	if closeN > 0 {
		day.CloseAvg = closeSum / float64(closeN)
		day.CloseWin = float64(winN) / float64(closeN)
	}
	if highN > 0 {
		day.HighAvg = highSum / float64(highN)
	}
	return day
}

// PremiumSnapshot 读某日（默认最近已收盘交易日）的昨涨停今溢价。
// 优先 limit_premium 快照；当日没有快照行时按 limit_board × market_data 实时兜底（Live=true）。
func (u *Usecase) PremiumSnapshot(ctx context.Context, day time.Time, days, top int) (*PremiumReport, error) {
	if days < 0 {
		days = 0
	}
	if days > 20 {
		days = 20
	}
	if top <= 0 {
		top = 50
	}
	if top > 200 {
		top = 200
	}
	report := &PremiumReport{Day: day, AsOf: u.now()}
	base, err := u.cal.PrevOpen(day)
	if err != nil {
		return nil, err
	}
	rows, err := u.repo.PremiumRows(ctx, day)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		rows, err = u.premiumLive(ctx, day, base)
		if err != nil {
			return nil, err
		}
		report.Live = len(rows) > 0
	}
	if len(rows) == 0 {
		return report, nil
	}
	report.Found = true
	for i := range rows {
		rows[i] = premiumRow(rows[i])
	}
	report.Summary = summarizePremium(rows)
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.HasClose != b.HasClose {
			return a.HasClose
		}
		if a.HasClose {
			return a.ClosePremium > b.ClosePremium
		}
		return a.OpenPremium > b.OpenPremium
	})
	if len(rows) > top {
		rows = rows[:top]
	}
	report.Rows = rows
	if days > 0 {
		from := day.AddDate(0, 0, -(days*7/5 + 10))
		history, err := u.repo.PremiumRange(ctx, from, day.AddDate(0, 0, -1))
		if err != nil {
			return nil, err
		}
		byDay := map[string][]PremiumRow{}
		order := []string{}
		for _, r := range history {
			r = premiumRow(r)
			k := keyDay(r.TradeDate)
			if _, ok := byDay[k]; !ok {
				order = append(order, k)
			}
			byDay[k] = append(byDay[k], r)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(order)))
		for _, k := range order {
			if len(report.Days) >= days {
				break
			}
			report.Days = append(report.Days, summarizePremium(byDay[k]))
		}
	}
	return report, nil
}

// premiumLive 是快照缺失时的兜底：昨日收盘涨停名单 × 今日日线（或竞价快照的开盘价）。
func (u *Usecase) premiumLive(ctx context.Context, day, base time.Time) ([]PremiumRow, error) {
	boards, err := u.repo.Boards(ctx, base, market.DirUp, market.StatusSealed)
	if err != nil {
		return nil, err
	}
	if len(boards) == 0 {
		return nil, nil
	}
	names, err := u.stockNames(ctx)
	if err != nil {
		return nil, err
	}
	bars, err := u.repo.DailyBarsOn(ctx, day)
	if err != nil {
		return nil, err
	}
	todaySealed, err := u.repo.SealedOn(ctx, day)
	if err != nil {
		return nil, err
	}
	now := u.now()
	out := make([]PremiumRow, 0, len(boards))
	for _, b := range boards {
		row := PremiumRow{
			Symbol: b.Symbol, Name: names[b.Symbol],
			TradeDate: day, BaseDate: base, Consecutive: b.Consecutive,
			BaseClose: b.LimitPrice, Source: "live", AsOf: now,
		}
		if bar, ok := bars[b.Symbol]; ok {
			row.Open, row.High, row.Close = bar.Open, bar.High, bar.Close
			if row.BaseClose <= 0 {
				row.BaseClose = bar.PreClose
			}
			row.Source = SourceLiveBar
		}
		if _, ok := todaySealed[b.Symbol]; ok {
			row.LimitAgain = true
		}
		out = append(out, row)
	}
	return out, nil
}

// PremiumOpen 是 09:27 的开盘溢价任务：昨日涨停名单 × 集合竞价开盘价 → limit_premium(open_premium)。
func (u *Usecase) PremiumOpen(ctx context.Context, now time.Time) error {
	day := market.DateOf(now)
	base, err := u.cal.PrevOpen(day)
	if err != nil {
		return err
	}
	boards, err := u.repo.Boards(ctx, base, market.DirUp, market.StatusSealed)
	if err != nil {
		return err
	}
	if len(boards) == 0 {
		return nil
	}
	names, err := u.stockNames(ctx)
	if err != nil {
		return err
	}
	opens := map[string]float64{}
	if u.snaps != nil {
		snaps, err := u.snaps.All(ctx)
		if err != nil {
			u.log.Warnf("premium.open: 快照源不可用：%v", err)
		} else {
			for _, s := range snaps {
				if s.Open > 0 && !s.Time.IsZero() && market.DateOf(s.Time) == day {
					opens[s.Symbol] = s.Open
				}
			}
		}
	}
	rows := make([]PremiumRow, 0, len(boards))
	for _, b := range boards {
		row := PremiumRow{
			Symbol: b.Symbol, Name: names[b.Symbol],
			TradeDate: day, BaseDate: base, Consecutive: b.Consecutive,
			BaseClose: b.LimitPrice, Source: SourceMarketAgg, AsOf: now,
		}
		if op, ok := opens[b.Symbol]; ok {
			row.Open = op
		}
		rows = append(rows, row)
	}
	return u.repo.SavePremiums(ctx, rows)
}

// PremiumClose 是收盘后的溢价定稿任务：补齐当日 OHLC 与收盘/最高溢价、今日是否再板。
func (u *Usecase) PremiumClose(ctx context.Context, now time.Time) error {
	day := market.DateOf(now)
	base, err := u.cal.PrevOpen(day)
	if err != nil {
		return err
	}
	boards, err := u.repo.Boards(ctx, base, market.DirUp, market.StatusSealed)
	if err != nil {
		return err
	}
	if len(boards) == 0 {
		return nil
	}
	names, err := u.stockNames(ctx)
	if err != nil {
		return err
	}
	bars, err := u.repo.DailyBarsOn(ctx, day)
	if err != nil {
		return err
	}
	todaySealed, err := u.repo.SealedOn(ctx, day)
	if err != nil {
		return err
	}
	rows := make([]PremiumRow, 0, len(boards))
	for _, b := range boards {
		row := PremiumRow{
			Symbol: b.Symbol, Name: names[b.Symbol],
			TradeDate: day, BaseDate: base, Consecutive: b.Consecutive,
			BaseClose: b.LimitPrice, Source: SourceMarketAgg, AsOf: now,
		}
		if bar, ok := bars[b.Symbol]; ok {
			row.Open, row.High, row.Close = bar.Open, bar.High, bar.Close
			if row.BaseClose <= 0 {
				row.BaseClose = bar.PreClose
			}
		}
		if _, ok := todaySealed[b.Symbol]; ok {
			row.LimitAgain = true
		}
		rows = append(rows, premiumRow(row))
	}
	return u.repo.SavePremiums(ctx, rows)
}

func (u *Usecase) stockNames(ctx context.Context) (map[string]string, error) {
	stocks, err := u.repo.LoadStocks(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(stocks))
	for _, s := range stocks {
		out[s.Symbol] = s.Name
	}
	return out, nil
}

/* ---------- 20 日均线上占比 ---------- */

// BreadthRecord 是 market_breadth 的一行。
type BreadthRecord struct {
	Day    time.Time
	Window int
	Above  int
	Below  int
	Flat   int
	Total  int
	Ratio  float64
	Source string
	AsOf   time.Time
}

// BreadthDay 是读口径的一天广度。Live 为真表示表里没有、实时算的。
type BreadthDay struct {
	Day    time.Time
	Window int
	Above  int
	Below  int
	Flat   int
	Total  int
	Ratio  float64
	Live   bool
	Source string
	AsOf   time.Time
}

// BreadthReport 是广度读结果。
type BreadthReport struct {
	Day    time.Time
	Found  bool
	Latest BreadthDay
	Days   []BreadthDay
}

const breadthWindow = 20

// BreadthSnapshot 读某日（默认最近已收盘交易日）的 20 日均线上占比。
// 表里有行就读表；当日没有行就实时算一次（不写库），保证首次上线当天界面不是空的。
func (u *Usecase) BreadthSnapshot(ctx context.Context, day time.Time, days int) (*BreadthReport, error) {
	if days <= 0 {
		days = 10
	}
	if days > 60 {
		days = 60
	}
	report := &BreadthReport{Day: day}
	window := u.openDays(ctx, day, days)
	if len(window) == 0 {
		return report, nil
	}
	rows, err := u.repo.BreadthRange(ctx, window[len(window)-1], day)
	if err != nil {
		return nil, err
	}
	byDay := make(map[string]BreadthRecord, len(rows))
	for _, r := range rows {
		byDay[keyDay(r.Day)] = r
	}
	for _, d := range window {
		rec, ok := byDay[keyDay(d)]
		if ok {
			report.Days = append(report.Days, BreadthDay{
				Day: rec.Day, Window: rec.Window, Above: rec.Above, Below: rec.Below,
				Flat: rec.Flat, Total: rec.Total, Ratio: rec.Ratio, AsOf: rec.AsOf,
			})
			continue
		}
		// 只有窗口里最新的一天才实时兜底：更早的日子补算成本高，且历史缺口应由任务回填。
		if keyDay(d) != keyDay(day) {
			continue
		}
		live, err := u.computeBreadth(ctx, d)
		if err != nil {
			return nil, err
		}
		if live.Total == 0 {
			continue
		}
		live.Live = true
		report.Days = append(report.Days, live)
	}
	if len(report.Days) > 0 {
		report.Found = true
		report.Latest = report.Days[0]
	}
	return report, nil
}

// BreadthClose 是收盘后的广度任务：算 20 日均线上占比并落 market_breadth。
func (u *Usecase) BreadthClose(ctx context.Context, now time.Time) error {
	day := market.DateOf(now)
	rec, err := u.computeBreadth(ctx, day)
	if err != nil {
		return err
	}
	if rec.Total == 0 {
		u.log.Warnf("breadth: %s 无日线样本，跳过", day.Format("2006-01-02"))
		return nil
	}
	rec.Source = SourceMarketAgg
	rec.AsOf = now
	return u.repo.SaveBreadth(ctx, rec)
}

// computeBreadth 对全市场算 MA20：样本 = 当日有日线且往前凑齐 window 根收盘的股票。
func (u *Usecase) computeBreadth(ctx context.Context, day time.Time) (BreadthDay, error) {
	out := BreadthDay{Day: day, Window: breadthWindow, AsOf: u.now()}
	hist, err := u.repo.DailyHistory(ctx, day.AddDate(0, 0, 1), breadthWindow+1)
	if err != nil {
		return out, err
	}
	dayKey := keyDay(day)
	for _, bars := range hist {
		if len(bars) < breadthWindow {
			continue
		}
		last := bars[len(bars)-1]
		if keyDay(last.Time) != dayKey || last.Close <= 0 {
			continue
		}
		var sum float64
		for _, b := range bars[len(bars)-breadthWindow:] {
			sum += b.Close
		}
		ma := sum / float64(breadthWindow)
		if ma <= 0 {
			continue
		}
		out.Total++
		switch {
		case last.Close > ma:
			out.Above++
		case last.Close < ma:
			out.Below++
		default:
			out.Flat++
		}
	}
	if out.Total > 0 {
		out.Ratio = float64(out.Above) / float64(out.Total)
	}
	return out, nil
}

/* ---------- 公共小件 ---------- */

// openDays 返回 ≤day 的最近 n 个交易日，按倒序（[0] 最新）。
// 迭代上限 200 次防止日历数据异常时无限倒退（一年 ~250 交易日）。
func (u *Usecase) openDays(ctx context.Context, day time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	cur := day
	for i := 0; len(out) < n && i < 200; i++ {
		if open, err := u.cal.Open(cur); err == nil && open {
			out = append(out, market.DateOf(cur))
		}
		prev, err := u.cal.PrevOpen(cur)
		if err != nil || !prev.Before(cur) {
			break
		}
		cur = prev
	}
	return out
}

func keyDay(t time.Time) string { return market.DateOf(t).Format("2006-01-02") }

func (u *Usecase) now() time.Time { return time.Now() }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
