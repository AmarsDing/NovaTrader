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

// EmotionPoint 盘中情绪路径的一个时点。
type EmotionPoint struct {
	Time         string // HH:MM（as_of）
	Auction      bool   // true 表示 09:25 竞价点（此时只填竞价字段）
	Up           int
	Down         int
	Broken       int
	BrokenRate   float64
	MaxHeight    int
	ProfitEffect float64
	Advance      int
	Decline      int
	Amount       float64
	Phase        string
	ScoreCoef    float64
	Stale        bool
}

// EmotionPath 一日盘中情绪路径：09:25 竞价头 + 当日全部情绪截面。
// 竞价强度 = 昨涨停池在今日竞价截面的平均涨幅；没取到为 nil（「没取到 ≠ 0」）。
type EmotionPath struct {
	Day                 time.Time
	AuctionFound        bool
	AuctionTotalAmount  float64
	AuctionLimitUpCount int
	PrevTierAuctionPct  *float64
	PrevTierSample      int
	Points              []EmotionPoint
	Partial             bool
	Missing             []string
}

// EmotionPath 汇总某日情绪路径。截面来自 checkpoint/close/auction job 的落库，
// 没存的时点不会在 points 出现（诚实呈现实际截面，不补零值点）。
func (u *Usecase) EmotionPath(ctx context.Context, day time.Time) (*EmotionPath, error) {
	p := &EmotionPath{Day: day}
	auc, err := u.AuctionCross(ctx, day, 0)
	if err != nil {
		return nil, err
	}
	if auc.Found {
		p.AuctionFound = true
		p.AuctionTotalAmount = auc.TotalAmount
		p.AuctionLimitUpCount = auc.LimitUpCount
	} else {
		p.Missing = append(p.Missing, "auction")
	}
	// 昨日涨停竞价强度
	prev, err := u.cal.PrevOpen(day)
	if err == nil {
		sealed, err2 := u.repo.SealedOn(ctx, prev)
		if err2 != nil {
			return nil, err2
		}
		if len(sealed) > 0 && auc.Found {
			asOf := time.Date(day.Year(), day.Month(), day.Day(), 9, 26, 0, 0, day.Location())
			rows, err3 := u.repo.FactorsAt(ctx, day, KindAuction, asOf, sealedSymbols(sealed))
			if err3 != nil {
				return nil, err3
			}
			sum, n := 0.0, 0
			for _, r := range rows {
				// 「没取到≠0」：缺 auction_pct 键（如新股无前收）只能记 partial，
				// 不得以 0 入均值把昨涨停竞价强度拉低——伪 0 会稀释真实读数
				if v, ok := r.Values["auction_pct"]; ok {
					sum += v
					n++
				}
			}
			if n > 0 {
				avg := r2x(sum / float64(n))
				p.PrevTierAuctionPct = &avg
				p.PrevTierSample = n
				if n < len(sealed) {
					p.Missing = append(p.Missing, "auction_prev_tier_partial")
				}
			}
		}
	}
	trail, err := u.repo.SentimentTrail(ctx, day)
	if err != nil {
		return nil, err
	}
	if len(trail) == 0 {
		p.Missing = append(p.Missing, "market_sentiment")
	}
	if auc.Found {
		p.Points = append(p.Points, EmotionPoint{Time: "09:25", Auction: true})
	}
	for _, s := range trail {
		p.Points = append(p.Points, EmotionPoint{
			Time: s.AsOf.Format("15:04"),
			Up:   s.UpCount, Down: s.DownCount, Broken: s.BrokenCount,
			BrokenRate: r2x(s.BrokenRate), MaxHeight: s.MaxHeight, ProfitEffect: r2x(s.ProfitEffect),
			Advance: s.Advance, Decline: s.Decline, Amount: s.Amount,
			Phase: string(s.Phase), ScoreCoef: r2x(s.ScoreCoef), Stale: s.Stale,
		})
	}
	if len(p.Missing) > 0 {
		p.Partial = true
	}
	return p, nil
}

func sealedSymbols(sealed map[string]int) []string {
	out := make([]string, 0, len(sealed))
	for s := range sealed {
		out = append(out, s)
	}
	return out
}

func r2x(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
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
