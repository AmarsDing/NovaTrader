package biz

import (
	"context"
	"fmt"
	"time"
)

// outcomeWindow 之前的候选不再回填。T+5 之后最多再等几天补数。
const outcomeWindow = 30 * 24 * time.Hour

// FillOutcomes 回填候选的 T+1/T+3/T+5 收益（入选和漏选都算）。收益 = 第 N 个交易日收盘 / ref_price − 1。
// 收盘价还没出来的那一档留空，下次再填。
func (uc *Usecase) FillOutcomes(ctx context.Context, now time.Time) (int, error) {
	today := tradeDay(now)
	list, err := uc.candidates.PendingOutcomes(ctx, today.Add(-outcomeWindow), today)
	if err != nil {
		return 0, err
	}
	filled := 0
	for _, c := range list {
		if c.RefPrice <= 0 {
			continue
		}
		days, err := uc.nextOpenDays(c.TradeDate, 5)
		if err != nil {
			return filled, err
		}
		closes, err := uc.market.DailyCloses(ctx, c.Symbol, days[0], days[4])
		if err != nil {
			return filled, fmt.Errorf("strategy: closes %s: %w", c.Symbol, err)
		}
		ret := func(n int, have *float64) *float64 {
			if have != nil {
				return have
			}
			d := days[n-1]
			if d.After(today) {
				return nil
			}
			px, ok := closes[d.Format("2006-01-02")]
			if !ok || px <= 0 {
				return nil
			}
			v := px/c.RefPrice - 1
			return &v
		}
		t1, t3, t5 := ret(1, c.RetT1), ret(3, c.RetT3), ret(5, c.RetT5)
		if t1 == c.RetT1 && t3 == c.RetT3 && t5 == c.RetT5 {
			continue
		}
		if err := uc.candidates.SetOutcome(ctx, c.ID, t1, t3, t5); err != nil {
			return filled, err
		}
		filled++
	}
	return filled, nil
}

// nextOpenDays 返回 day 之后的 n 个交易日。
func (uc *Usecase) nextOpenDays(day time.Time, n int) ([]time.Time, error) {
	out := make([]time.Time, 0, n)
	d := tradeDay(day)
	for len(out) < n {
		next, err := uc.cal.NextOpen(d)
		if err != nil {
			return nil, err
		}
		out = append(out, next)
		d = next
	}
	return out, nil
}
