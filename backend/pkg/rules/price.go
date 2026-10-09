package rules

import (
	"errors"
	"fmt"

	"server/pkg/ashare"
)

var (
	ErrNoATR  = errors.New("rules: not enough bars for ATR")
	ErrLevels = errors.New("rules: price levels collapsed")
)

// PriceParams 是入场区间、止损、止盈、卖出限价和仓位建议的参数。
type PriceParams struct {
	ATRPeriod        int
	StopATR          float64
	TakeATR          float64
	MaxStopPct       float64
	EntryBandATR     float64
	SellSlipPct      float64
	PositionPerStock float64
	NormalFactor     float64
}

func DefaultPrice() PriceParams {
	return PriceParams{
		ATRPeriod:        14,
		StopATR:          2,
		TakeATR:          3,
		MaxStopPct:       0.07,
		EntryBandATR:     0.25,
		SellSlipPct:      0.005,
		PositionPerStock: 0.05,
		NormalFactor:     0.6,
	}
}

func LoadPrice(get Lookup) PriceParams {
	d := DefaultPrice()
	return PriceParams{
		ATRPeriod:        int(pick(get, "m06.atr_period", float64(d.ATRPeriod))),
		StopATR:          pick(get, "m06.stop_atr", d.StopATR),
		TakeATR:          pick(get, "m06.take_atr", d.TakeATR),
		MaxStopPct:       pick(get, "m06.max_stop_pct", d.MaxStopPct),
		EntryBandATR:     pick(get, "m06.entry_band_atr", d.EntryBandATR),
		SellSlipPct:      pick(get, "m06.sell_slip_pct", d.SellSlipPct),
		PositionPerStock: pick(get, "m06.position_per_stock", d.PositionPerStock),
		NormalFactor:     pick(get, "m06.normal_position_factor", d.NormalFactor),
	}
}

// Levels 是一条买入信号的价格。全部已取整到分，并在信号当日涨跌停之间。
type Levels struct {
	Entry      float64
	EntryLow   float64
	EntryHigh  float64
	StopLoss   float64
	TakeProfit float64
	ATR        float64
	LimitUp    float64
	LimitDown  float64
}

// Buy 以 ref 为参考价计算买入价位。结果不满足 止损 < 下沿 ≤ 入场 ≤ 上沿 < 止盈 时返回 ErrLevels。
func (p PriceParams) Buy(s Snapshot, ref float64) (Levels, error) {
	atr, ok := ATR(s.Bars, p.ATRPeriod)
	if !ok || atr <= 0 {
		return Levels{}, ErrNoATR
	}
	up, down, err := todayLimits(s)
	if err != nil {
		return Levels{}, err
	}
	entry := ashare.Clamp(ref, up, down)
	band := p.EntryBandATR * atr
	stop := entry - p.StopATR*atr
	if floor := entry * (1 - p.MaxStopPct); p.MaxStopPct > 0 && stop < floor {
		stop = floor
	}
	lv := Levels{
		Entry:      entry,
		EntryLow:   ashare.Clamp(entry-band, up, down),
		EntryHigh:  ashare.Clamp(entry+band, up, down),
		StopLoss:   ashare.Clamp(stop, up, down),
		TakeProfit: ashare.Clamp(entry+p.TakeATR*atr, up, down),
		ATR:        atr,
		LimitUp:    up,
		LimitDown:  down,
	}
	if p.MaxStopPct > 0 && lv.StopLoss < entry*(1-p.MaxStopPct)-eps {
		lv.StopLoss = ashare.Clamp(lv.StopLoss+ashare.Tick, up, down)
	}
	if !(lv.StopLoss < lv.EntryLow-eps && lv.EntryLow <= lv.Entry && lv.Entry <= lv.EntryHigh && lv.EntryHigh < lv.TakeProfit-eps) {
		return lv, fmt.Errorf("%w: stop=%.2f low=%.2f entry=%.2f high=%.2f take=%.2f",
			ErrLevels, lv.StopLoss, lv.EntryLow, lv.Entry, lv.EntryHigh, lv.TakeProfit)
	}
	return lv, nil
}

// SellPrice 是卖出限价：当前价下浮 SellSlipPct，不低于跌停价。
func (p PriceParams) SellPrice(s Snapshot) (float64, error) {
	_, down, err := todayLimits(s)
	if err != nil {
		return 0, err
	}
	return ashare.Clamp(s.Last()*(1-p.SellSlipPct), 0, down), nil
}

// PositionPct 是建议仓位占总权益的比例。上限和情绪缩放由 M08 处理。
func (p PriceParams) PositionPct(high bool) float64 {
	if high {
		return p.PositionPerStock
	}
	return p.PositionPerStock * p.NormalFactor
}
