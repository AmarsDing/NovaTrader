package rules

import (
	"context"
	"fmt"
	"math"
)

const NamePullback = "pullback_dip"

// PullbackParams 是模板二「强势回踩」的阈值，见 M06 设计文档 4.2。
type PullbackParams struct {
	Lookback   int     // 看强势和高点的交易日数
	StrongGain float64 // 区间涨幅达到即算强势
	DDMin      float64 // 距高点回撤下限
	DDMax      float64 // 距高点回撤上限
	DDIdeal    float64 // 打分时最理想的回撤
	MABand     float64 // 当前价与 MA10 的最大偏离
	ShrinkMax  float64 // 昨日量 / 前 5 日均量 的上限
	ReboundMin float64 // 当前价高出当日最低的比例下限
	FloorPct   float64 // 当前价不低于昨收的比例
	HoldDays   int
}

func DefaultPullback() PullbackParams {
	return PullbackParams{
		Lookback:   10,
		StrongGain: 0.20,
		DDMin:      0.08,
		DDMax:      0.20,
		DDIdeal:    0.12,
		MABand:     0.03,
		ShrinkMax:  0.7,
		ReboundMin: 0.01,
		FloorPct:   0.98,
		HoldDays:   5,
	}
}

func LoadPullback(get Lookup) PullbackParams {
	d := DefaultPullback()
	return PullbackParams{
		Lookback:   int(pick(get, "m06.pd.lookback", float64(d.Lookback))),
		StrongGain: pick(get, "m06.pd.strong_gain", d.StrongGain),
		DDMin:      pick(get, "m06.pd.dd_min", d.DDMin),
		DDMax:      pick(get, "m06.pd.dd_max", d.DDMax),
		DDIdeal:    pick(get, "m06.pd.dd_ideal", d.DDIdeal),
		MABand:     pick(get, "m06.pd.ma_band", d.MABand),
		ShrinkMax:  pick(get, "m06.pd.shrink_max", d.ShrinkMax),
		ReboundMin: pick(get, "m06.pd.rebound_min", d.ReboundMin),
		FloorPct:   pick(get, "m06.pd.floor_pct", d.FloorPct),
		HoldDays:   int(pick(get, "m06.pd.hold_days", float64(d.HoldDays))),
	}
}

// Pullback 是模板二：近期强势股缩量回到 MA10 附近后止跌。
type Pullback struct {
	P     PullbackParams
	Price PriceParams
}

func NewPullback(p PullbackParams, price PriceParams) *Pullback {
	return &Pullback{P: p, Price: price}
}

func (p *Pullback) Name() string { return NamePullback }

type pdSetup struct {
	dd       float64
	volRatio float64
	trend    float64
	limits   int
}

func (p *Pullback) setup(s Snapshot) (pdSetup, bool) {
	n := len(s.Bars)
	if p.P.Lookback < 2 || n < 20 || n < p.P.Lookback+1 || n < avgWindow+1 {
		return pdSetup{}, false
	}
	from := n - p.P.Lookback
	peak, maxClose := 0.0, 0.0
	for _, b := range s.Bars[from:] {
		peak = math.Max(peak, b.High)
		maxClose = math.Max(maxClose, b.Close)
	}
	if s.Today != nil && s.Today.High > peak {
		return pdSetup{}, false
	}
	limitsHit := sealedCount(s, from, n)
	base := s.Bars[from-1].Close
	if limitsHit == 0 && (base <= 0 || maxClose/base-1 < p.P.StrongGain) {
		return pdSetup{}, false
	}
	last := s.Last()
	if peak <= 0 || last <= 0 {
		return pdSetup{}, false
	}
	dd := 1 - last/peak
	if dd < p.P.DDMin-eps || dd > p.P.DDMax+eps {
		return pdSetup{}, false
	}
	ma10, _ := MA(s.Bars, 10)
	ma20, _ := MA(s.Bars, 20)
	if ma20 <= 0 || ma10 <= ma20 || last < ma20-eps {
		return pdSetup{}, false
	}
	if math.Abs(last/ma10-1) > p.P.MABand+eps {
		return pdSetup{}, false
	}
	vbase, ok := avg(s.Bars, n-1-avgWindow, n-1, volumeOf)
	if !ok || vbase <= 0 {
		return pdSetup{}, false
	}
	ratio := float64(s.Bars[n-1].Volume) / vbase
	if ratio > p.P.ShrinkMax+eps {
		return pdSetup{}, false
	}
	return pdSetup{dd: dd, volRatio: ratio, trend: ma10/ma20 - 1, limits: limitsHit}, true
}

func (p *Pullback) trigger(s Snapshot) bool {
	if s.Today == nil || s.Today.Low <= 0 {
		return false
	}
	last := s.Last()
	return last >= s.Today.Low*(1+p.P.ReboundMin)-eps && last >= s.PrevClose()*p.P.FloorPct-eps
}

// Filter 盘前只看形态；盘中还要满足止跌。
func (p *Pullback) Filter(_ context.Context, s Snapshot) bool {
	if _, ok := p.setup(s); !ok {
		return false
	}
	return s.Today == nil || p.trigger(s)
}

func (p *Pullback) Score(_ context.Context, s Snapshot) float64 {
	st, ok := p.setup(s)
	if !ok {
		return 0
	}
	score := 40.0
	span := math.Max(p.P.DDIdeal-p.P.DDMin, p.P.DDMax-p.P.DDIdeal)
	if span > 0 {
		score += clamp(1-math.Abs(st.dd-p.P.DDIdeal)/span, 0, 1) * 15
	}
	switch {
	case st.volRatio <= 0.5:
		score += 15
	case p.P.ShrinkMax > 0.5:
		score += 15 - (st.volRatio-0.5)/(p.P.ShrinkMax-0.5)*10
	}
	score += clamp(st.trend, 0, 0.05) / 0.05 * 15
	switch {
	case st.limits >= 2:
		score += 10
	case st.limits == 1:
		score += 5
	}
	if prev := s.PrevClose(); s.Today != nil && prev > 0 {
		score += clamp((s.Last()-s.Today.Low)/prev, 0, 0.03) / 0.03 * 5
	}
	return clamp(score, 0, 100)
}

func (p *Pullback) EntryPlan(ctx context.Context, s Snapshot) (Plan, bool) {
	if s.Today == nil || !p.Filter(ctx, s) {
		return Plan{}, false
	}
	st, _ := p.setup(s)
	reason := fmt.Sprintf("近 %d 日强势（涨停 %d 次），距高点回撤 %.1f%%，昨日缩量至 %.2f 倍，回到 MA10 附近后止跌",
		p.P.Lookback, st.limits, st.dd*100, st.volRatio)
	return Plan{Side: SideBuy, Price: s.Last(), Reason: reason, HoldDays: p.P.HoldDays}, true
}

func (p *Pullback) ExitPlan(_ context.Context, s Snapshot, pos Position) (Plan, bool) {
	return Exit(s, pos, p.P.HoldDays, p.Price)
}
