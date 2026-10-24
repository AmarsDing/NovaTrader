package rules

// Shape 是「起没起来」形态三态加 missing（B9，仿 TSP new-term-radar 判定思路，
// 供选股漏斗前置滤 AI 与 R6/R8 题材热度共用）。missing 只表达取数不足，
// 绝不冒充 holding（数据纪律「没取到≠0」）。
type Shape string

const (
	ShapeRising  Shape = "rising"
	ShapeHolding Shape = "holding"
	ShapeFading  Shape = "fading"
	ShapeMissing Shape = "missing"
)

// ShapeOf 判定近窗相对基线的形态。series 旧→新：近窗取尾部 recent 根的均值，
// 基线取近窗之前紧邻 base 根的均值，两窗不重叠。偏移 = 近窗均值/基线均值 - 1：
// 偏移 > band 判 rising、< -band 判 fading、带内（含 ±band 压边）归 holding——
// 压边不改判，与告警 above/below 严格比较同例。
// 任一窗不足根、recent/base 非正、band 为负或 NaN、窗内含非正值
// （0/负值是取数缺口不是数据）→ ShapeMissing。
func ShapeOf(series []float64, recent, base int, band float64) Shape {
	if recent <= 0 || base <= 0 || !(band >= 0) || len(series) < recent+base {
		return ShapeMissing
	}
	win := series[len(series)-(recent+base):]
	baseAvg, ok := meanPositive(win[:base])
	if !ok {
		return ShapeMissing
	}
	recentAvg, ok := meanPositive(win[base:])
	if !ok {
		return ShapeMissing
	}
	dev := recentAvg/baseAvg - 1
	switch {
	case dev > band:
		return ShapeRising
	case dev < -band:
		return ShapeFading
	default:
		return ShapeHolding
	}
}

// meanPositive 求均值；空窗或任一元素非正时 ok 为假——价格与热度域里
// 0/负值都是取数缺口，不允许参与均值冒充低位（数据纪律）。
func meanPositive(xs []float64) (float64, bool) {
	if len(xs) == 0 {
		return 0, false
	}
	sum := 0.0
	for _, v := range xs {
		if !(v > 0) {
			return 0, false
		}
		sum += v
	}
	return sum / float64(len(xs)), true
}

// closesOf 把快照拼成收盘价序列：已收盘日线旧→新，当日已取价时收尾。
// Today.Close 非正视为当日未取到而剔除（与 Snapshot.Last 同口径），
// 不拿 0 价污染序列。
func closesOf(s Snapshot) []float64 {
	closes := make([]float64, 0, len(s.Bars)+1)
	for _, b := range s.Bars {
		closes = append(closes, b.Close)
	}
	if s.Today != nil && s.Today.Close > 0 {
		closes = append(closes, s.Today.Close)
	}
	return closes
}

// ShapeOfSnapshot 由快照直判形态：「起没起来」正是盘中问题，当日价参与近窗。
func ShapeOfSnapshot(s Snapshot, recent, base int, band float64) Shape {
	return ShapeOf(closesOf(s), recent, base, band)
}

// ShapeParams 是 B9 形态前置滤 AI 的参数，默认值即首发口径。
type ShapeParams struct {
	Recent int     // 近窗根数（默认 3）
	Base   int     // 基线窗根数（默认 10）
	Band   float64 // 偏移阈值（默认 0.03 = 3%）；扫描层以 Band<=0 关停本件
}

// LoadShape 读取 m06.shape.* 键；缺键或解析失败用默认值。
// 键值范围由 pkg/params 登记把关（shape.recent/shape.base 1..250 整数、
// shape.band 0..1），这里只做容错回退，不重复校验。
func LoadShape(get Lookup) ShapeParams {
	p := ShapeParams{Recent: 3, Base: 10, Band: 0.03}
	p.Recent = int(pick(get, "m06.shape.recent", float64(p.Recent)))
	p.Base = int(pick(get, "m06.shape.base", float64(p.Base)))
	p.Band = pick(get, "m06.shape.band", p.Band)
	return p
}
