package biz

import (
	"fmt"
	"math"
	"time"

	"server/pkg/symbol"
)

// 质量检查名，与 data_quality_issue.check_name 一致。
const (
	CheckEmpty        = "empty"
	CheckStale        = "stale"
	CheckOutlier      = "outlier"
	CheckTimestamp    = "timestamp"
	CheckCompleteness = "completeness"
	CheckReconcile    = "reconcile"
)

// Issue 是一条质量问题。Symbol 为空表示整域问题。
type Issue struct {
	Domain      string
	Check       string
	Symbol      string
	TradeDate   time.Time
	Severity    string
	Source      string
	OtherSource string
	Message     string
	Detail      map[string]any
}

// SnapshotRules 是快照校验参数。
type SnapshotRules struct {
	Now        time.Time
	StaleAfter time.Duration
	// Trading 为真时检查行情时间是否落后；集合竞价和连续竞价时段为真。
	Trading  bool
	MinCount int
}

// CheckSnapshots 清洗一轮快照：隔离结构/未来时间异常，逐证券标记行情过期。
// 旧价和无成交记录保留供查看，不把零成交推断为停牌或替它生成新时间。
// 整批没有新鲜行情时失败；一只股票的当前时间不能替其余股票保鲜。
// 返回的 error 会让本次源调用算失败，从而切到下一源。
func CheckSnapshots(items []Snapshot, r SnapshotRules) ([]Snapshot, []Issue, error) {
	if len(items) == 0 {
		return nil, nil, fmt.Errorf("快照为空")
	}
	var issues []Issue
	out := items[:0:0]
	fresh := 0
	future := 0
	for _, s := range items {
		if _, err := symbol.Parse(s.Symbol); err != nil {
			continue
		}
		if s.Last < 0 || s.High < 0 || s.Low < 0 || s.Volume < 0 || s.Amount < 0 || (s.High > 0 && s.Low > 0 && s.High < s.Low) {
			issues = append(issues, Issue{Check: CheckOutlier, Symbol: s.Symbol, Severity: "warn",
				Message: fmt.Sprintf("快照价量不合法：last=%v high=%v low=%v vol=%v", s.Last, s.High, s.Low, s.Volume)})
			continue
		}
		if !s.Time.IsZero() && s.Time.After(r.Now.Add(time.Minute)) {
			future++
			continue
		}
		reason := ""
		switch {
		case s.Stale:
			reason = "源头已标记行情过期"
		case s.Time.IsZero():
			reason = "快照没有源行情时间"
		case r.Trading && r.StaleAfter > 0 && r.Now.Sub(s.Time) > r.StaleAfter:
			reason = fmt.Sprintf("源行情时间 %s，落后 %s", s.Time.In(shanghai()).Format("2006-01-02 15:04:05"), r.Now.Sub(s.Time).Round(time.Second))
		}
		if reason != "" {
			s.Stale = true
			issues = append(issues, Issue{Check: CheckStale, Symbol: s.Symbol, Severity: "warn", Message: reason})
		} else {
			fresh++
		}
		out = append(out, s)
	}
	if future > 0 {
		issues = append(issues, Issue{Check: CheckTimestamp, Severity: "warn",
			Message: fmt.Sprintf("%d 条快照的行情时间晚于当前时刻，已丢弃", future)})
	}
	if r.MinCount > 0 && len(out) < r.MinCount {
		return out, issues, fmt.Errorf("快照只有 %d 条，少于 %d", len(out), r.MinCount)
	}
	if r.Trading && r.StaleAfter > 0 {
		if fresh == 0 {
			return out, issues, &StaleError{Reason: "快照没有可用的新鲜源行情时间"}
		}
	}
	return out, issues, nil
}

// CheckBars 清洗一批 K 线：价格非正、高低倒挂、成交量为负、时间不在请求范围的丢弃；
// 相对前收涨跌超过 maxPct 的保留但记异常值（新股、复牌可能真实出现）。
func CheckBars(domain string, bars []Bar, q BarQuery, maxPct float64) ([]Bar, []Issue) {
	var issues []Issue
	out := bars[:0:0]
	start := q.Start.In(shanghai())
	end := q.End.In(shanghai()).AddDate(0, 0, 1)
	for _, b := range bars {
		bad := ""
		switch {
		case b.Open <= 0 || b.High <= 0 || b.Low <= 0 || b.Close <= 0:
			bad = "价格非正"
		case b.High < b.Low || b.High < math.Max(b.Open, b.Close)-1e-6 || b.Low > math.Min(b.Open, b.Close)+1e-6:
			bad = "高低价与开收不一致"
		case b.Volume < 0 || b.Amount < 0:
			bad = "成交量或成交额为负"
		case !q.Start.IsZero() && (b.Time.Before(start) || !b.Time.Before(end)):
			bad = "时间不在请求范围"
		}
		if bad != "" {
			check := CheckOutlier
			if bad == "时间不在请求范围" {
				check = CheckTimestamp
			}
			issues = append(issues, Issue{Domain: domain, Check: check, Symbol: b.Symbol, TradeDate: dateOf(b.Time),
				Severity: "warn", Message: fmt.Sprintf("%s %s：%s", b.Freq, b.Time.In(shanghai()).Format("2006-01-02 15:04"), bad)})
			continue
		}
		if maxPct > 0 && b.PreClose != nil && *b.PreClose > 0 {
			pct := math.Abs(b.Close / *b.PreClose - 1)
			if pct > maxPct {
				issues = append(issues, Issue{Domain: domain, Check: CheckOutlier, Symbol: b.Symbol, TradeDate: dateOf(b.Time),
					Severity: "warn", Message: fmt.Sprintf("涨跌幅 %.1f%% 超过 %.0f%%，已保留待核对", pct*100, maxPct*100)})
			}
		}
		out = append(out, b)
	}
	return out, issues
}

// Completeness 计算覆盖率：got 里出现的代码占 expect 的比例。expect 为空时返回 1。
func Completeness(got []string, expect []string) (float64, []string) {
	if len(expect) == 0 {
		return 1, nil
	}
	have := make(map[string]struct{}, len(got))
	for _, s := range got {
		have[s] = struct{}{}
	}
	var missing []string
	for _, s := range expect {
		if _, ok := have[s]; !ok {
			missing = append(missing, s)
		}
	}
	return 1 - float64(len(missing))/float64(len(expect)), missing
}

func dateOf(t time.Time) time.Time {
	d := t.In(shanghai())
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, shanghai())
}
