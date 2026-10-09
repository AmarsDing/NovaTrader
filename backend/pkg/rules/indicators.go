package rules

import (
	"math"
	"time"

	"server/pkg/ashare"
)

const eps = 1e-9

// MA 是最近 n 根收盘价的简单平均。根数不足时 ok 为假。
func MA(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n {
		return 0, false
	}
	sum := 0.0
	for _, b := range bars[len(bars)-n:] {
		sum += b.Close
	}
	return sum / float64(n), true
}

// ATR 是最近 n 根真实波幅的简单平均，与通达信 ATR 同口径。需要 n+1 根日线。
func ATR(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n+1 {
		return 0, false
	}
	sum := 0.0
	for i := len(bars) - n; i < len(bars); i++ {
		prev := bars[i-1].Close
		b := bars[i]
		tr := math.Max(b.High-b.Low, math.Max(math.Abs(b.High-prev), math.Abs(b.Low-prev)))
		sum += tr
	}
	return sum / float64(n), true
}

// avg 对 bars[from:to] 取平均。区间越界或为空时 ok 为假。
func avg(bars []Bar, from, to int, f func(Bar) float64) (float64, bool) {
	if from < 0 || to > len(bars) || from >= to {
		return 0, false
	}
	sum := 0.0
	for _, b := range bars[from:to] {
		sum += f(b)
	}
	return sum / float64(to-from), true
}

func amountOf(b Bar) float64 { return b.Amount }
func volumeOf(b Bar) float64 { return float64(b.Volume) }

// limits 返回 day 当天以 prev 为基准的涨停价和跌停价。幅度按当天的交易规则取，回测不会用到今天的规则。
func limits(symbol string, st bool, prev float64, day time.Time) (up, down float64, err error) {
	ratio, err := ashare.RatioOn(symbol, st, day)
	if err != nil {
		return 0, 0, err
	}
	up, down = ashare.Limit(prev, ratio)
	return up, down, nil
}

// todayLimits 是快照决策日的涨跌停价。
func todayLimits(s Snapshot) (up, down float64, err error) {
	return limits(s.Symbol, s.ST, s.PrevClose(), s.AsOf)
}

// dayLimit 判断 bars[i] 是否触及或收在涨停。i 必须 ≥ 1，用前一根收盘做基准。
// K 线没有时间时按 asOf 的规则算。
func dayLimit(symbol string, st bool, bars []Bar, i int, asOf time.Time) (touched, sealed bool) {
	if i < 1 || i >= len(bars) {
		return false, false
	}
	day := bars[i].Time
	if day.IsZero() {
		day = asOf
	}
	up, _, err := limits(symbol, st, bars[i-1].Close, day)
	if err != nil || up <= 0 {
		return false, false
	}
	b := bars[i]
	return b.High >= up-eps, b.Close >= up-eps
}

// sealedCount 数 s.Bars[from:to] 里收盘涨停的根数。
func sealedCount(s Snapshot, from, to int) int {
	if from < 1 {
		from = 1
	}
	if to > len(s.Bars) {
		to = len(s.Bars)
	}
	n := 0
	for i := from; i < to; i++ {
		if _, sealed := dayLimit(s.Symbol, s.ST, s.Bars, i, s.AsOf); sealed {
			n++
		}
	}
	return n
}
