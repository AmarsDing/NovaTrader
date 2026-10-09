// Package refstrat 是 M07 用来验收回测引擎的确定性参考策略，不用于实盘。
// 规则见 doc/开发文档/M07-回测与自进化/设计文档.md 第 5 节。
package refstrat

import (
	"context"
	"math"

	"server/pkg/ashare"
	"server/pkg/rules"
)

const Name = "breakout_ref"

func init() {
	rules.Register(rules.Spec{
		Name:  Name,
		Title: "参考策略：N 日新高突破",
		Params: []rules.ParamSpec{
			{Name: "lookback", Title: "突破回看天数", Default: 20, Min: 5, Max: 60, Step: 5},
			{Name: "stop_loss", Title: "止损比例", Default: 0.05, Min: 0.02, Max: 0.10, Step: 0.01},
			{Name: "take_profit", Title: "止盈比例", Default: 0.10, Min: 0.04, Max: 0.20, Step: 0.02},
			{Name: "max_hold", Title: "最长持有交易日", Default: 3, Min: 1, Max: 10, Step: 1},
			{Name: "min_amount", Title: "近 5 日日均成交额（元）", Default: 1e8, Min: 5e7, Max: 5e8, Step: 5e7},
		},
		New: func(p map[string]float64) (rules.Strategy, error) {
			return &Breakout{
				Lookback:   int(p["lookback"]),
				StopLoss:   p["stop_loss"],
				TakeProfit: p["take_profit"],
				MaxHold:    int(p["max_hold"]),
				MinAmount:  p["min_amount"],
			}, nil
		},
	})
}

type Breakout struct {
	Lookback   int
	StopLoss   float64
	TakeProfit float64
	MaxHold    int
	MinAmount  float64
}

func (b *Breakout) Name() string { return Name }

func (b *Breakout) Filter(_ context.Context, s rules.Snapshot) bool {
	if s.ST || s.Suspended || len(s.Bars) < b.Lookback {
		return false
	}
	n := len(s.Bars)
	from := n - 5
	if from < 0 {
		from = 0
	}
	sum := 0.0
	for _, bar := range s.Bars[from:] {
		sum += bar.Amount
	}
	return sum/float64(n-from) >= b.MinAmount
}

// high 是已收盘日线最近 lookback 根的最高价。
func (b *Breakout) high(s rules.Snapshot) float64 {
	from := len(s.Bars) - b.Lookback
	if from < 0 {
		from = 0
	}
	h := 0.0
	for _, bar := range s.Bars[from:] {
		h = math.Max(h, bar.High)
	}
	return h
}

func (b *Breakout) Score(_ context.Context, s rules.Snapshot) float64 {
	h := b.high(s)
	if h <= 0 {
		return 0
	}
	return math.Min(100, s.Last()/h*50)
}

// EntryPlan 当前价突破前高、且没到涨停价时买入。限价 = min(当前价 × 1.01, 涨停价)。
func (b *Breakout) EntryPlan(_ context.Context, s rules.Snapshot) (rules.Plan, bool) {
	if s.Today == nil || len(s.Bars) < b.Lookback {
		return rules.Plan{}, false
	}
	last, h := s.Last(), b.high(s)
	if last <= 0 || h <= 0 || last <= h {
		return rules.Plan{}, false
	}
	ratio, err := ashare.RatioOn(s.Symbol, s.ST, s.AsOf)
	if err != nil {
		return rules.Plan{}, false
	}
	up, down := ashare.Limit(s.PrevClose(), ratio)
	if last >= up {
		return rules.Plan{}, false
	}
	return rules.Plan{
		Side:     rules.SideBuy,
		Price:    ashare.Clamp(last*1.01, up, down),
		Reason:   "突破前高",
		HoldDays: b.MaxHold,
	}, true
}

func (b *Breakout) ExitPlan(_ context.Context, s rules.Snapshot, pos rules.Position) (rules.Plan, bool) {
	last := s.Last()
	if s.Today == nil || last <= 0 || pos.Available <= 0 {
		return rules.Plan{}, false
	}
	plan := rules.Plan{Side: rules.SideSell, Shares: pos.Available}
	switch {
	case last <= pos.AvgCost*(1-b.StopLoss):
		plan.Reason, plan.ExitKind = "止损", rules.ExitStopLoss
	case last >= pos.AvgCost*(1+b.TakeProfit):
		plan.Reason, plan.ExitKind = "止盈", rules.ExitTakeProfit
	case pos.HoldDays >= b.MaxHold && late(s):
		plan.Reason, plan.ExitKind = "时间止损", rules.ExitTime
	default:
		return rules.Plan{}, false
	}
	return plan, true
}

// late 盘中要到 14:50 之后才做时间止损。日线回测的快照时刻是 15:00，直接成立。
func late(s rules.Snapshot) bool {
	t := s.AsOf
	return t.Hour()*60+t.Minute() >= 14*60+50
}
