package market

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// 下面是通达信公式函数的逐字实现（整列向量运算），用来核对递推结果。
// NaN 表示该位置无值。

func ref(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	for i := n; i < len(x); i++ {
		out[i] = x[i-n]
	}
	return out
}

func tdxMA(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	for i := range x {
		if i+1 < n {
			continue
		}
		s, ok := 0.0, true
		for j := i - n + 1; j <= i; j++ {
			if math.IsNaN(x[j]) {
				ok = false
			}
			s += x[j]
		}
		if ok {
			out[i] = s / float64(n)
		}
	}
	return out
}

func tdxEMA(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	a := 2 / float64(n+1)
	for i, v := range x {
		if math.IsNaN(v) {
			continue
		}
		if i == 0 || math.IsNaN(out[i-1]) {
			out[i] = v
			continue
		}
		out[i] = a*v + (1-a)*out[i-1]
	}
	return out
}

func tdxSMA(x []float64, n, m int) []float64 {
	out := nanSlice(len(x))
	for i, v := range x {
		if math.IsNaN(v) {
			continue
		}
		if i == 0 || math.IsNaN(out[i-1]) {
			out[i] = v
			continue
		}
		out[i] = (float64(m)*v + float64(n-m)*out[i-1]) / float64(n)
	}
	return out
}

func tdxSTD(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	for i := range x {
		if i+1 < n {
			continue
		}
		mean := 0.0
		for j := i - n + 1; j <= i; j++ {
			mean += x[j]
		}
		mean /= float64(n)
		ss := 0.0
		for j := i - n + 1; j <= i; j++ {
			ss += (x[j] - mean) * (x[j] - mean)
		}
		out[i] = math.Sqrt(ss / float64(n-1))
	}
	return out
}

func nanSlice(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
	}
	return out
}

func randomBars(n int, seed int64) []DayBar {
	r := rand.New(rand.NewSource(seed))
	px := 20.0
	adj := 1.0
	day := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	var out []DayBar
	for i := 0; i < n; i++ {
		pre := px
		if i == n/2 {
			// 除权：10 送 5，前收盘按交易所口径调整，后复权因子同比放大。
			pre = math.Round(px/1.5*100) / 100
			adj *= px / pre
		}
		c := math.Round(pre*(1+(r.Float64()-0.5)*0.1)*100) / 100
		o := math.Round(pre*(1+(r.Float64()-0.5)*0.04)*100) / 100
		h := math.Max(c, o) * (1 + r.Float64()*0.02)
		l := math.Min(c, o) * (1 - r.Float64()*0.02)
		v := int64(1e6 + r.Intn(1e6))
		out = append(out, DayBar{
			Time: day.AddDate(0, 0, i), Open: o, High: h, Low: l, Close: c, PreClose: pre,
			Volume: v, Amount: float64(v) * c, Adj: adj, FloatShare: 1e8,
		})
		px = c
	}
	return out
}

func near(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b))
}

func TestIndicatorsMatchTdxFormulas(t *testing.T) {
	bars := randomBars(300, 7)
	n := len(bars)
	last := bars[n-1].Adj
	// 通达信前复权：价 × 当日因子 ÷ 最新因子。
	C, H, L := make([]float64, n), make([]float64, n), make([]float64, n)
	for i, b := range bars {
		k := b.Adj / last
		C[i], H[i], L[i] = b.Close*k, b.High*k, b.Low*k
	}
	LC := ref(C, 1)
	up, abs, tr := make([]float64, n), make([]float64, n), make([]float64, n)
	for i := range C {
		if math.IsNaN(LC[i]) {
			up[i], abs[i], tr[i] = math.NaN(), math.NaN(), math.NaN()
			continue
		}
		up[i] = math.Max(C[i]-LC[i], 0)
		abs[i] = math.Abs(C[i] - LC[i])
		tr[i] = math.Max(math.Max(H[i]-L[i], math.Abs(LC[i]-H[i])), math.Abs(LC[i]-L[i]))
	}
	dif := make([]float64, n)
	e12, e26 := tdxEMA(C, 12), tdxEMA(C, 26)
	for i := range dif {
		dif[i] = e12[i] - e26[i]
	}
	dea := tdxEMA(dif, 9)
	rsi6Up, rsi6Abs := tdxSMA(up, 6, 1), tdxSMA(abs, 6, 1)
	atr := tdxMA(tr, 14)
	mid, std := tdxMA(C, 20), tdxSTD(C, 20)
	ma60 := tdxMA(C, 60)

	got := Series(bars, AdjustForward)
	for i := range bars {
		v := got[i]
		check := func(key string, want float64) {
			t.Helper()
			x, ok := v[key]
			if math.IsNaN(want) {
				if ok {
					t.Fatalf("bar %d %s should be absent, got %v", i, key, x)
				}
				return
			}
			if !ok || !near(x, want) {
				t.Fatalf("bar %d %s = %v (ok=%v), want %v", i, key, x, ok, want)
			}
		}
		check("ma60", ma60[i])
		check("dif", dif[i])
		check("dea", dea[i])
		check("macd", 2*(dif[i]-dea[i]))
		check("atr14", atr[i])
		check("boll_mid", mid[i])
		if !math.IsNaN(mid[i]) {
			check("boll_up", mid[i]+2*std[i])
		}
		if !math.IsNaN(rsi6Up[i]) && rsi6Abs[i] > 0 {
			check("rsi6", rsi6Up[i]/rsi6Abs[i]*100)
		}
	}
}

func TestPreviewEqualsPush(t *testing.T) {
	bars := randomBars(80, 3)
	a, b := NewIndicators(), NewIndicators()
	for i, bar := range bars {
		p := a.Preview(bar, BarsPerDay)
		q := b.Push(bar)
		a.Push(bar)
		for k, x := range q {
			if !near(p[k], x) {
				t.Fatalf("bar %d key %s preview %v push %v", i, k, p[k], x)
			}
		}
		if len(p) != len(q) {
			t.Fatalf("bar %d preview has %d keys, push %d", i, len(p), len(q))
		}
	}
}

func TestPointInTimeUnaffectedByLaterExRights(t *testing.T) {
	bars := randomBars(300, 11)
	cut := len(bars)/2 - 1 // 除权日之前
	before := Series(bars[:cut+1], AdjustForward)[cut]
	pit := Series(bars, AdjustNone)[cut]
	for _, k := range []string{"ma20", "dif", "atr14", "boll_up"} {
		if !near(before[k], pit[k]) {
			t.Fatalf("%s: value seen that day %v, recomputed later %v", k, before[k], pit[k])
		}
	}
}

func TestVolumeRatioTurnoverIntraday(t *testing.T) {
	ind := NewIndicators()
	for i := 0; i < 5; i++ {
		ind.Push(DayBar{Close: 10, High: 10, Low: 10, PreClose: 10, Volume: 2400})
	}
	v := ind.Preview(DayBar{Close: 10, High: 10, Low: 10, PreClose: 10, Volume: 300, Amount: 3000, FloatShare: 30000}, 30)
	// 前 5 日每分钟 10 股，开盘 30 分钟应有 300 股，量比 1。
	if !near(v["vol_ratio"], 1) || !near(v["turnover"], 1) || !near(v["vwap"], 10) {
		t.Fatal(v)
	}
}
