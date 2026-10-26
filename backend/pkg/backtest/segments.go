package backtest

import "math"

// W8c 接线：候选×段时间绩效采集（CSCV 的矩阵原料）。
//
// 数据纪律：段指标只从逐日净值现算；段内收益非有限、净值非正、或段内波动
// 低于噪声地板，一律该段记 NaN——「没取到≠0」，绝不补 0 冒充绩效。
//
// 落库纪律：encoding/json 不能表达 NaN/Inf（marshal 硬错误会把整份报告顶飞），
// 故 evaluate 侧在写 Point.Segments 前过 finiteOrNil 消毒——含非有限值的候选
// 整条段链置 nil 不落键；CSCV 对这类候选本就整行剔除，二者统计语义等价，
// 但报告 JSON 不再被单个空仓/爆仓候选毒化（审查批 wf_9932525c 高危销号）。

// segmentSharpe 把净值曲线按日序等宽切成 seg 段，返回每段的日收益 Sharpe
// （均值 / 总体标准差；不年化——year 常数乘子秩一致，秩比较只吃相对大小；
// 风险口径与 Compute 对齐：日超额收益 = 日收益 − riskFree/252）。
//
// 口径：
//   - 日收益 r_i = equity[i]/prev − 1 − riskFree/252，首日 prev 取 initial（与
//     Compute 同基线）；
//   - 段 j 覆盖半开窗口 [⌊j·n/seg⌋, ⌊(j+1)·n/seg⌋)（整数除法无缝铺满 [0,n)，
//     哪些日归哪段只由该窗口决定，确定性不依赖时钟）；
//   - 段内标准差 < 1e-6（水平线/尘埃段）→ 该段 NaN：1e-6 是日收益波动的经济
//     噪声地板（远低于 1bp），tick 级微动或停牌微漂的 sd 只有 1e-8 量级，
//     放行会被 mean/sd 无界放大成离群 Sharpe 劫持 CSCV 的秩比较（审查批低危销号）；
//   - prev ≤ 0 或收益比非有限 → 该日所在段整段 NaN（不硬解半截段）。
//
// 返回 nil：seg < 4、seg 为奇数、initial ≤ 0、或曲线装不下每段两日（len < seg*2）。
func segmentSharpe(eq []EquityPoint, initial float64, seg int, riskFree float64) []float64 {
	if seg < 4 || seg%2 != 0 || initial <= 0 || len(eq) < seg*2 {
		return nil
	}
	rfDaily := riskFree / tradingDaysPerYear
	n := len(eq)
	out := make([]float64, seg)
	for j := 0; j < seg; j++ {
		var sum, sumSq float64
		cnt := 0
		bad := false
		for i := j * n / seg; i < (j+1)*n/seg; i++ {
			prev := initial
			if i > 0 {
				prev = eq[i-1].Equity
			}
			if prev <= 0 {
				bad = true
				break
			}
			r := eq[i].Equity/prev - 1 - rfDaily
			if math.IsNaN(r) || math.IsInf(r, 0) {
				bad = true
				break
			}
			sum += r
			sumSq += r * r
			cnt++
		}
		if bad || cnt == 0 {
			out[j] = math.NaN()
			continue
		}
		mean := sum / float64(cnt)
		variance := sumSq/float64(cnt) - mean*mean
		if variance < 0 {
			variance = 0 // 浮点消去误差下界钳制（真实方差恒 ≥0）
		}
		sd := math.Sqrt(variance)
		if sd < 1e-6 {
			out[j] = math.NaN()
			continue
		}
		out[j] = mean / sd
	}
	return out
}

// finiteOrNil 把含 NaN/Inf 的段链整条置 nil（evaluate 写 Point.Segments 前的
// 落库消毒，omitempty 下 nil 不落键）。语义等价于 CSCV 的 NaN 行剔除——该候选
// 不进过拟合矩阵；同时保住 encoding/json 的 marshal（NaN 会硬报错连累整份报告）。
// 全有限或 nil 原样返回。
func finiteOrNil(s []float64) []float64 {
	for _, v := range s {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
	}
	return s
}
