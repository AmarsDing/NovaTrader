package biz

import (
	"context"
	"sort"
	"time"
)

// AuctionRow 是一只股票的集合竞价截面（stock_factor kind=auction，09:25 落库）。
type AuctionRow struct {
	Symbol     string
	Pct        float64
	Amount     float64
	VolRatio   float64
	Turnover   float64
	LimitUp    bool
	Unmatched  float64
}

// AuctionCross 是某日集合竞价汇总。Found 为假表示该日还没有竞价因子。
type AuctionCross struct {
	Day          time.Time
	Found        bool
	AsOf         time.Time
	TotalAmount  float64
	LimitUpCount int
	LimitUps     []AuctionRow
	TopRush      []AuctionRow
}

func auctionFloat(values map[string]float64, key string) float64 {
	if values == nil {
		return 0
	}
	return values[key]
}

// AuctionCross 汇总某日集合竞价截面：竞价总额、竞价涨停、抢筹前 N（默认 10，上限 50）。
// 数据来自 09:25 落库的 stock_factor(kind=auction)；没有行时 Found=false，前端写「当日无竞价截面」，不编数。
func (u *Usecase) AuctionCross(ctx context.Context, day time.Time, top int) (*AuctionCross, error) {
	if top <= 0 {
		top = 10
	}
	if top > 50 {
		top = 50
	}
	// 竞价行 as_of=09:25；用当日 09:26 作为上限，避免拿到盘中重写的同 kind 行
	asOf := time.Date(day.Year(), day.Month(), day.Day(), 9, 26, 0, 0, day.Location())
	rows, err := u.repo.FactorsAt(ctx, day, KindAuction, asOf, nil)
	if err != nil {
		return nil, err
	}
	summary := &AuctionCross{Day: day}
	if len(rows) == 0 {
		return summary, nil
	}
	summary.Found = true
	all := make([]AuctionRow, 0, len(rows))
	for _, r := range rows {
		row := AuctionRow{
			Symbol:    r.Symbol,
			Pct:       auctionFloat(r.Values, "auction_pct"),
			Amount:    auctionFloat(r.Values, "auction_amount"),
			VolRatio:  auctionFloat(r.Values, "auction_vol_ratio"),
			Turnover:  auctionFloat(r.Values, "auction_turnover"),
			LimitUp:   auctionFloat(r.Values, "auction_limit_up") > 0.5,
			Unmatched: auctionFloat(r.Values, "auction_unmatched"),
		}
		if row.Amount > 0 {
			summary.TotalAmount += row.Amount
		}
		if row.LimitUp {
			summary.LimitUpCount++
			summary.LimitUps = append(summary.LimitUps, row)
		}
		if r.AsOf.After(summary.AsOf) {
			summary.AsOf = r.AsOf
		}
		all = append(all, row)
	}
	byAmount := func(list []AuctionRow) {
		sort.Slice(list, func(i, j int) bool { return list[i].Amount > list[j].Amount })
	}
	byAmount(summary.LimitUps)
	byAmount(all)
	if len(all) > top {
		all = all[:top]
	}
	summary.TopRush = all
	return summary, nil
}

// LadderStep 是一档晋级统计：前一日 N 连板、次日封住 N+1 的比例。
type LadderStep struct {
	From      int
	Sample    int
	Succeeded int
	Rate      float64
}

// LadderDay 是一个「次日」的梯队表现。
type LadderDay struct {
	Day   time.Time
	Steps []LadderStep
}

// Ladder 统计最近 days 个交易日（默认 5，上限 20）的连板晋级率：1进2、2进3、3进4。
// 每个交易日 t：取 t-1 收盘连板表（SealedOn），其中连板数=k 的股票，若 t 日仍封板且连板数=k+1，记晋级。
// 样本为 0 的档 Rate=0，由前端写「无样本」。3 板以上合并进 3进4（连板数≥3 且次日≥4 算晋级）。
func (u *Usecase) Ladder(ctx context.Context, to time.Time, days int) ([]LadderDay, []LadderStep, error) {
	if days <= 0 {
		days = 5
	}
	if days > 20 {
		days = 20
	}
	out := make([]LadderDay, 0, days)
	total := map[int]*LadderStep{1: {From: 1}, 2: {From: 2}, 3: {From: 3}}
	cur := to
	for len(out) < days {
		prev, err := u.cal.PrevOpen(cur)
		if err != nil {
			return nil, nil, err
		}
		prevSealed, err := u.repo.SealedOn(ctx, prev)
		if err != nil {
			return nil, nil, err
		}
		curSealed, err := u.repo.SealedOn(ctx, cur)
		if err != nil {
			return nil, nil, err
		}
		if len(prevSealed) == 0 {
			// 基准日没有连板数据（没采集/没收盘）：这一天不纳入统计，继续往前。
			// 基准日有、次日没有视为次日全部未晋级（采集缺失按失败计，样本口径在 Steps 里可见）。
			cur = prev
			continue
		}
		steps := map[int]*LadderStep{1: {From: 1}, 2: {From: 2}, 3: {From: 3}}
		for sym, h := range prevSealed {
			bucket := h
			if bucket > 3 {
				bucket = 3
			}
			step := steps[bucket]
			step.Sample++
			total[bucket].Sample++
			next := curSealed[sym]
			want := h + 1
			if h >= 3 {
				want = 4 // 3 板以上次日 ≥4 连板算晋级
			}
			if next >= want {
				step.Succeeded++
				total[bucket].Succeeded++
			}
		}
		day := LadderDay{Day: cur}
		for _, k := range []int{1, 2, 3} {
			s := steps[k]
			if s.Sample > 0 {
				s.Rate = float64(s.Succeeded) / float64(s.Sample)
			}
			day.Steps = append(day.Steps, *s)
		}
		out = append(out, day)
		cur = prev
	}
	totals := make([]LadderStep, 0, 3)
	for _, k := range []int{1, 2, 3} {
		s := total[k]
		if s.Sample > 0 {
			s.Rate = float64(s.Succeeded) / float64(s.Sample)
		}
		totals = append(totals, *s)
	}
	return out, totals, nil
}
