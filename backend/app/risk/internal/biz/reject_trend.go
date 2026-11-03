package biz

import (
	"context"
	"time"

	"server/pkg/tradecal"
)

// BE-11 拒绝聚合窗口：默认 7 天，上限 31 天。
const (
	defaultTrendDays = 7
	maxTrendDays     = 31
)

// RejectDay 是一个上海日的校验与拒绝计数。
type RejectDay struct {
	Date     string
	Checks   int
	Rejected int
}

// RejectTrend 是窗口聚合结果：Days 稀疏（仅 data 层返回时），Rules 是窗口内按规则的拒绝计数。
type RejectTrend struct {
	Days  []RejectDay
	Rules map[string]int
}

// RejectTrend 返回 end 起回看 days 天（含 end）的每日拒绝序列与按规则聚合。
// end 为空取今天（上海时区）；days<=0 取 7，>31 截断为 31。返回的日期序列升序且连续（无数据日补 0）。
func (e *Engine) RejectTrend(ctx context.Context, end string, days int) (string, int, *RejectTrend, error) {
	day := e.now().In(tradecal.Shanghai())
	if end != "" {
		d, err := time.ParseInLocation("2006-01-02", end, tradecal.Shanghai())
		if err != nil {
			return "", 0, nil, invalid("end must be YYYY-MM-DD")
		}
		day = d
	}
	if days <= 0 {
		days = defaultTrendDays
	}
	if days > maxTrendDays {
		days = maxTrendDays
	}
	endDay := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, tradecal.Shanghai())
	from := endDay.AddDate(0, 0, -(days - 1))
	t, err := e.repo.RejectTrend(ctx, from, endDay.AddDate(0, 0, 1))
	if err != nil {
		return "", 0, nil, err
	}
	if t == nil {
		t = &RejectTrend{}
	}
	byDate := make(map[string]RejectDay, len(t.Days))
	for _, d := range t.Days {
		byDate[d.Date] = d
	}
	full := make([]RejectDay, 0, days)
	for i := 0; i < days; i++ {
		date := from.AddDate(0, 0, i).Format("2006-01-02")
		d := byDate[date]
		d.Date = date
		full = append(full, d)
	}
	t.Days = full
	return endDay.Format("2006-01-02"), days, t, nil
}
