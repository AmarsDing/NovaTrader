package backtest

import (
	"math/rand"
	"sort"
)

// MonteCarloResult 是日收益分块重采样的分位数。
type MonteCarloResult struct {
	Runs        int     `json:"runs"`
	Block       int     `json:"block"`
	ReturnP5    float64 `json:"return_p5"`
	ReturnP50   float64 `json:"return_p50"`
	ReturnP95   float64 `json:"return_p95"`
	DrawdownP5  float64 `json:"drawdown_p5"`
	DrawdownP50 float64 `json:"drawdown_p50"`
	DrawdownP95 float64 `json:"drawdown_p95"`
	LossProb    float64 `json:"loss_prob"`
	DDThreshold float64 `json:"dd_threshold"`
	DDBreach    float64 `json:"dd_breach"`
}

// MonteCarlo 从日收益里按块长 block 有放回抽块，拼成与原区间等长的序列，重复 runs 次。
func MonteCarlo(initial float64, equity []EquityPoint, runs, block int, seed int64, ddThreshold float64) *MonteCarloResult {
	rets := dailyReturns(initial, equity)
	n := len(rets)
	if n == 0 || runs <= 0 {
		return nil
	}
	if block <= 0 {
		block = 5
	}
	if block > n {
		block = n
	}
	rng := rand.New(rand.NewSource(seed))
	totals := make([]float64, runs)
	dds := make([]float64, runs)
	loss, breach := 0, 0
	for r := 0; r < runs; r++ {
		eq, peak, dd := 1.0, 1.0, 0.0
		for filled := 0; filled < n; {
			start := rng.Intn(n - block + 1)
			for k := start; k < start+block && filled < n; k++ {
				eq *= 1 + rets[k]
				if eq > peak {
					peak = eq
				}
				if d := (peak - eq) / peak; d > dd {
					dd = d
				}
				filled++
			}
		}
		totals[r], dds[r] = eq-1, dd
		if eq < 1 {
			loss++
		}
		if ddThreshold > 0 && dd > ddThreshold {
			breach++
		}
	}
	sort.Float64s(totals)
	sort.Float64s(dds)
	q := func(v []float64, p float64) float64 { return v[int(p*float64(len(v)-1))] }
	return &MonteCarloResult{
		Runs: runs, Block: block,
		ReturnP5: q(totals, 0.05), ReturnP50: q(totals, 0.5), ReturnP95: q(totals, 0.95),
		DrawdownP5: q(dds, 0.05), DrawdownP50: q(dds, 0.5), DrawdownP95: q(dds, 0.95),
		LossProb:    float64(loss) / float64(runs),
		DDThreshold: ddThreshold,
		DDBreach:    float64(breach) / float64(runs),
	}
}
