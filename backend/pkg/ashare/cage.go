package ashare

import (
	"math"

	"server/pkg/symbol"
)

// MaxOrderVolume 是单笔限价申报的股数上限。主板、北交所 100 万股，创业板 30 万股，科创板 10 万股。
func MaxOrderVolume(raw string) (int, error) {
	sym, err := symbol.Parse(raw)
	if err != nil {
		return 0, err
	}
	switch {
	case isSTAR(sym.Code):
		return 100_000, nil
	case sym.Market == "SZ" && len(sym.Code) >= 2 && sym.Code[:2] == "30":
		return 300_000, nil
	default:
		return 1_000_000, nil
	}
}

// CageBenchmark 返回连续竞价价格笼子的基准价。
// 买入：卖一，没有则买一，再没有则最新价，当日无成交用前收。卖出：买一、卖一、最新价、前收。
func CageBenchmark(sell bool, bid1, ask1, last, prevClose float64) float64 {
	order := []float64{ask1, bid1, last, prevClose}
	if sell {
		order = []float64{bid1, ask1, last, prevClose}
	}
	for _, p := range order {
		if p > 0 {
			return p
		}
	}
	return 0
}

// Cage 返回连续竞价阶段的有效申报价格边界：买入时是上限，卖出时是下限。
// 主板、创业板：基准价 ±2% 与 ±10 个价位取宽。科创板：±2%，没有 10 个价位兜底。
// 北交所：±5% 与 ±10 个价位取宽。边界按分取整到合法一侧：上限向下取，下限向上取。
func Cage(raw string, sell bool, benchmark float64) (float64, error) {
	sym, err := symbol.Parse(raw)
	if err != nil {
		return 0, err
	}
	bench := int64(math.Round(benchmark * 100))
	if bench <= 0 {
		return 0, nil
	}
	pct, floorTicks := int64(2), int64(10)
	switch {
	case isSTAR(sym.Code):
		floorTicks = 0
	case sym.Market == "BJ":
		pct = 5
	}
	if !sell {
		byPct := bench * (100 + pct) / 100
		if byTick := bench + floorTicks; byTick > byPct {
			byPct = byTick
		}
		return float64(byPct) / 100, nil
	}
	byPct := (bench*(100-pct) + 99) / 100
	if byTick := bench - floorTicks; byTick < byPct {
		byPct = byTick
	}
	if byPct < 1 {
		byPct = 1
	}
	return float64(byPct) / 100, nil
}

// OnTick 判断价格是否落在 0.01 元价位上。
func OnTick(price float64) bool {
	c := price * 100
	return math.Abs(c-math.Round(c)) < 1e-6
}
