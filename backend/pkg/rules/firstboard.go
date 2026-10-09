package rules

import (
	"context"
	"fmt"
)

const NameFirstBoard = "first_board_weak2strong"

// FirstBoardParams 是模板一「首板 / 弱转强」的阈值，见 M06 设计文档 4.1。
type FirstBoardParams struct {
	NoLimitDays int     // 昨日之前多少个交易日内不能有收盘涨停
	AmountRatio float64 // 昨日成交额 / 前 5 日均额 的下限
	GapMin      float64 // 今日开盘涨幅下限
	GapMax      float64 // 今日开盘涨幅上限
	StrengthMin float64 // 当前涨幅下限
	BigAmount   float64 // 昨日成交额达到即加分，元
	HoldDays    int     // 最长持有交易日
}

func DefaultFirstBoard() FirstBoardParams {
	return FirstBoardParams{
		NoLimitDays: 5,
		AmountRatio: 1.5,
		GapMin:      0,
		GapMax:      0.06,
		StrengthMin: 0.02,
		BigAmount:   3e8,
		HoldDays:    2,
	}
}

func LoadFirstBoard(get Lookup) FirstBoardParams {
	d := DefaultFirstBoard()
	return FirstBoardParams{
		NoLimitDays: int(pick(get, "m06.fb.no_limit_days", float64(d.NoLimitDays))),
		AmountRatio: pick(get, "m06.fb.amount_ratio", d.AmountRatio),
		GapMin:      pick(get, "m06.fb.gap_min", d.GapMin),
		GapMax:      pick(get, "m06.fb.gap_max", d.GapMax),
		StrengthMin: pick(get, "m06.fb.strength_min", d.StrengthMin),
		BigAmount:   pick(get, "m06.fb.big_amount", d.BigAmount),
		HoldDays:    int(pick(get, "m06.fb.hold_days", float64(d.HoldDays))),
	}
}

const avgWindow = 5

// FirstBoard 是模板一：昨日首次触板，今日高开高走。
type FirstBoard struct {
	P     FirstBoardParams
	Price PriceParams
}

func NewFirstBoard(p FirstBoardParams, price PriceParams) *FirstBoard {
	return &FirstBoard{P: p, Price: price}
}

func (f *FirstBoard) Name() string { return NameFirstBoard }

type fbSetup struct {
	sealed bool
	ratio  float64
	amount float64
}

func (f *FirstBoard) setup(s Snapshot) (fbSetup, bool) {
	n := len(s.Bars)
	y := n - 1
	need := f.P.NoLimitDays
	if need < avgWindow {
		need = avgWindow
	}
	if y-need < 1 {
		return fbSetup{}, false
	}
	touched, sealed := dayLimit(s.Symbol, s.ST, s.Bars, y, s.AsOf)
	if !touched {
		return fbSetup{}, false
	}
	if sealedCount(s, y-f.P.NoLimitDays, y) > 0 {
		return fbSetup{}, false
	}
	base, ok := avg(s.Bars, y-avgWindow, y, amountOf)
	if !ok || base <= 0 {
		return fbSetup{}, false
	}
	ratio := s.Bars[y].Amount / base
	if ratio < f.P.AmountRatio {
		return fbSetup{}, false
	}
	return fbSetup{sealed: sealed, ratio: ratio, amount: s.Bars[y].Amount}, true
}

func (f *FirstBoard) trigger(s Snapshot) bool {
	prev := s.PrevClose()
	if s.Today == nil || prev <= 0 || s.Today.Open <= 0 {
		return false
	}
	gap := s.Today.Open/prev - 1
	if gap < f.P.GapMin-eps || gap > f.P.GapMax+eps {
		return false
	}
	last := s.Last()
	return last >= s.Today.Open-eps && last/prev-1 >= f.P.StrengthMin-eps
}

// Filter 盘前只看昨日形态；盘中还要满足高开高走。
func (f *FirstBoard) Filter(_ context.Context, s Snapshot) bool {
	if _, ok := f.setup(s); !ok {
		return false
	}
	return s.Today == nil || f.trigger(s)
}

func (f *FirstBoard) Score(_ context.Context, s Snapshot) float64 {
	st, ok := f.setup(s)
	if !ok {
		return 0
	}
	score := 40.0
	if st.sealed {
		score += 15
	} else {
		score += 10
	}
	score += clamp(st.ratio/3, 0, 1) * 15
	if st.amount >= f.P.BigAmount {
		score += 5
	}
	prev := s.PrevClose()
	if s.Today != nil && prev > 0 && s.Today.Open > 0 {
		gap := s.Today.Open/prev - 1
		switch {
		case gap >= 0.01-eps && gap <= 0.04+eps:
			score += 15
		case gap >= f.P.GapMin-eps && gap <= f.P.GapMax+eps:
			score += 8
		}
		score += clamp((s.Last()-s.Today.Open)/prev, 0, 0.05) / 0.05 * 10
	}
	return clamp(score, 0, 100)
}

func (f *FirstBoard) EntryPlan(ctx context.Context, s Snapshot) (Plan, bool) {
	if s.Today == nil || !f.Filter(ctx, s) {
		return Plan{}, false
	}
	st, _ := f.setup(s)
	kind := "炸板"
	if st.sealed {
		kind = "封板"
	}
	prev := s.PrevClose()
	reason := fmt.Sprintf("昨日首板%s，放量 %.1f 倍；今日高开 %.1f%%，现涨 %.1f%%",
		kind, st.ratio, (s.Today.Open/prev-1)*100, (s.Last()/prev-1)*100)
	return Plan{Side: SideBuy, Price: s.Last(), Reason: reason, HoldDays: f.P.HoldDays}, true
}

func (f *FirstBoard) ExitPlan(_ context.Context, s Snapshot, pos Position) (Plan, bool) {
	return Exit(s, pos, f.P.HoldDays, f.Price)
}
