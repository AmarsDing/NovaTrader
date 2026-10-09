package market

import (
	"math"
	"time"
)

// DayBar 是指标的输入：一根不复权日线和当日后复权因子。
type DayBar struct {
	Symbol     string
	Time       time.Time
	Open       float64
	High       float64
	Low        float64
	Close      float64
	PreClose   float64 // 交易所前收盘，0 时用上一根收盘
	Volume     int64
	Amount     float64
	Adj        float64 // 后复权因子，0 视为 1
	FloatShare int64   // 流通股本，0 时不算换手
}

// Values 是一根线上的指标值。窗口不足的键不出现。
type Values map[string]float64

// linearKeys 随价格等比缩放，复权换基准时要除以因子。
var linearKeys = []string{
	"ma5", "ma10", "ma20", "ma60", "ema12", "ema26", "dif", "dea", "macd",
	"atr14", "boll_mid", "boll_up", "boll_low",
}

// Rebase 把后复权指标换成以 adj 为基准的价格：除以当日因子得到当时的前复权值。
func (v Values) Rebase(adj float64) Values {
	if adj <= 0 || adj == 1 {
		return v
	}
	for _, k := range linearKeys {
		if x, ok := v[k]; ok {
			v[k] = x / adj
		}
	}
	return v
}

var (
	maPeriods  = []int{5, 10, 20, 60}
	rsiPeriods = []int{6, 12, 24}
)

const (
	window      = 60
	atrPeriod   = 14
	bollPeriod  = 20
	bollWidth   = 2
	volPeriod   = 5
	emaFast     = 12
	emaSlow     = 26
	deaPeriod   = 9
	minutesFull = BarsPerDay
)

type ema struct {
	alpha float64
	v     float64
	ok    bool
}

func newEMA(n int) ema { return ema{alpha: 2 / float64(n+1)} }

func (e ema) next(x float64) float64 {
	if !e.ok {
		return x
	}
	return e.alpha*x + (1-e.alpha)*e.v
}

// sma 是通达信 SMA(X,N,M)：Y=(M×X+(N−M)×Y')/N，首值为 X。
type sma struct {
	n, m float64
	v    float64
	ok   bool
}

func (s sma) next(x float64) float64 {
	if !s.ok {
		return x
	}
	return (s.m*x + (s.n-s.m)*s.v) / s.n
}

type rsiState struct {
	up, abs sma
}

// ring 保存最近 cap 个值，老的在前。
type ring struct {
	buf  []float64
	head int
	n    int
}

func newRing(capacity int) ring { return ring{buf: make([]float64, capacity)} }

func (r *ring) push(x float64) {
	r.buf[r.head] = x
	r.head = (r.head + 1) % len(r.buf)
	if r.n < len(r.buf) {
		r.n++
	}
}

// back(0) 是最新值。
func (r *ring) back(i int) float64 {
	idx := (r.head - 1 - i + 2*len(r.buf)) % len(r.buf)
	return r.buf[idx]
}

// Indicators 是一只股票的日线指标状态。Push 提交一根已收盘日线；Preview 用盘中临时日线试算，不改状态。
type Indicators struct {
	closes ring // 后复权收盘
	trs    ring
	vols   ring
	fast   ema
	slow   ema
	dea    ema
	rsi    []rsiState
	lastC  float64 // 上一根后复权收盘
	lastRC float64 // 上一根不复权收盘
	count  int
}

func NewIndicators() *Indicators {
	ind := &Indicators{
		closes: newRing(window),
		trs:    newRing(atrPeriod),
		vols:   newRing(volPeriod),
		fast:   newEMA(emaFast),
		slow:   newEMA(emaSlow),
		dea:    newEMA(deaPeriod),
	}
	for _, n := range rsiPeriods {
		ind.rsi = append(ind.rsi, rsiState{up: sma{n: float64(n), m: 1}, abs: sma{n: float64(n), m: 1}})
	}
	return ind
}

// Count 是已提交的日线根数。
func (ind *Indicators) Count() int { return ind.count }

// Push 提交一根日线，返回它的后复权指标。
func (ind *Indicators) Push(b DayBar) Values {
	return ind.step(b, minutesFull, true)
}

// Preview 用当日临时日线试算。minutes 是已开盘分钟数，只影响量比。
func (ind *Indicators) Preview(b DayBar, minutes int) Values {
	return ind.step(b, minutes, false)
}

func adjOf(b DayBar) float64 {
	if b.Adj <= 0 {
		return 1
	}
	return b.Adj
}

func (ind *Indicators) step(b DayBar, minutes int, commit bool) Values {
	adj := adjOf(b)
	c := b.Close * adj
	h := b.High * adj
	l := b.Low * adj
	out := Values{}

	ma := func(n int) (float64, bool) {
		if ind.closes.n+1 < n {
			return 0, false
		}
		sum := c
		for i := 0; i < n-1; i++ {
			sum += ind.closes.back(i)
		}
		return sum / float64(n), true
	}
	for _, n := range maPeriods {
		if v, ok := ma(n); ok {
			out[maKey(n)] = v
		}
	}

	fast := ind.fast.next(c)
	slow := ind.slow.next(c)
	dif := fast - slow
	dea := ind.dea.next(dif)
	out["ema12"], out["ema26"] = fast, slow
	out["dif"], out["dea"], out["macd"] = dif, dea, 2*(dif-dea)

	hasPrev := ind.count > 0
	var tr float64
	if hasPrev {
		lc := ind.lastC
		tr = math.Max(math.Max(h-l, math.Abs(lc-h)), math.Abs(lc-l))
		if ind.trs.n+1 >= atrPeriod {
			sum := tr
			for i := 0; i < atrPeriod-1; i++ {
				sum += ind.trs.back(i)
			}
			out["atr14"] = sum / atrPeriod
		}
		diff := c - lc
		for i, n := range rsiPeriods {
			up := ind.rsi[i].up.next(math.Max(diff, 0))
			abs := ind.rsi[i].abs.next(math.Abs(diff))
			if abs > 0 {
				out[rsiKey(n)] = up / abs * 100
			}
		}
	}

	if mid, ok := ma(bollPeriod); ok {
		ss := (c - mid) * (c - mid)
		for i := 0; i < bollPeriod-1; i++ {
			d := ind.closes.back(i) - mid
			ss += d * d
		}
		std := math.Sqrt(ss / (bollPeriod - 1))
		out["boll_mid"], out["boll_up"], out["boll_low"] = mid, mid+bollWidth*std, mid-bollWidth*std
	}

	pre := b.PreClose
	if pre <= 0 && hasPrev {
		pre = ind.lastRC
	}
	if pre > 0 {
		out["pct_chg"] = (b.Close - pre) / pre * 100
		out["amplitude"] = (b.High - b.Low) / pre * 100
	}
	if ind.vols.n >= volPeriod {
		sum := 0.0
		for i := 0; i < volPeriod; i++ {
			sum += ind.vols.back(i)
		}
		if minutes <= 0 || minutes > minutesFull {
			minutes = minutesFull
		}
		base := sum / volPeriod / minutesFull * float64(minutes)
		if base > 0 {
			out["vol_ratio"] = float64(b.Volume) / base
		}
	}
	if b.FloatShare > 0 {
		out["turnover"] = float64(b.Volume) / float64(b.FloatShare) * 100
	}
	if b.Volume > 0 {
		out["vwap"] = b.Amount / float64(b.Volume)
	}

	if commit {
		ind.closes.push(c)
		if hasPrev {
			ind.trs.push(tr)
			diff := c - ind.lastC
			for i := range ind.rsi {
				ind.rsi[i].up.v, ind.rsi[i].up.ok = ind.rsi[i].up.next(math.Max(diff, 0)), true
				ind.rsi[i].abs.v, ind.rsi[i].abs.ok = ind.rsi[i].abs.next(math.Abs(diff)), true
			}
		}
		ind.vols.push(float64(b.Volume))
		ind.fast.v, ind.fast.ok = fast, true
		ind.slow.v, ind.slow.ok = slow, true
		ind.dea.v, ind.dea.ok = dea, true
		ind.lastC = c
		ind.lastRC = b.Close
		ind.count++
	}
	return out
}

func maKey(n int) string {
	switch n {
	case 5:
		return "ma5"
	case 10:
		return "ma10"
	case 20:
		return "ma20"
	default:
		return "ma60"
	}
}

func rsiKey(n int) string {
	switch n {
	case 6:
		return "rsi6"
	case 12:
		return "rsi12"
	default:
		return "rsi24"
	}
}

// Adjust 是查询指标或 K 线时的复权方式。
type Adjust int

const (
	// AdjustNone 价格不复权；指标按每根线当日的因子换算，即每天当时看到的值。
	AdjustNone Adjust = iota
	// AdjustForward 前复权，以序列最后一根的因子为基准，与通达信默认一致。
	AdjustForward
	// AdjustBackward 后复权。
	AdjustBackward
)

// Series 用同一套递推算整段日线的指标，供查询接口和回测使用。
func Series(bars []DayBar, mode Adjust) []Values {
	ind := NewIndicators()
	out := make([]Values, len(bars))
	for i, b := range bars {
		out[i] = ind.Push(b)
	}
	if len(bars) == 0 {
		return out
	}
	base := adjOf(bars[len(bars)-1])
	for i, v := range out {
		switch mode {
		case AdjustForward:
			v.Rebase(base)
		case AdjustNone:
			v.Rebase(adjOf(bars[i]))
		}
	}
	return out
}

// AdjustBar 按复权方式换算一根 K 线的价格。base 是前复权基准因子。
func AdjustBar(b Bar, adj, base float64, mode Adjust) Bar {
	if adj <= 0 {
		adj = 1
	}
	var k float64
	switch mode {
	case AdjustForward:
		if base <= 0 {
			base = 1
		}
		k = adj / base
	case AdjustBackward:
		k = adj
	default:
		return b
	}
	b.Open *= k
	b.High *= k
	b.Low *= k
	b.Close *= k
	return b
}
