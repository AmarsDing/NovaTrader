package market

import "time"

// Quote 是一条外围收盘。TradeDate 为当地交易日。
type Quote struct {
	Code      string
	TradeDate time.Time
	PctChg    float64
	AsOf      time.Time
}

// Mapping 是外围标的到板块的映射。
type Mapping struct {
	Asset  string
	Sector string
	Weight float64
}

// Impulse 计算板块外围冲击：Σ 权重 × 涨跌幅%。
// 每个标的只取 TradeDate 不早于 A 股上一交易日、且 AsOf 早于 cutoff 的最新一条，避免重复计入已经反映过的行情。
func Impulse(quotes []Quote, mappings []Mapping, prevTradeDay, cutoff time.Time) map[string]float64 {
	floor := dateOf(prevTradeDay)
	latest := map[string]Quote{}
	for _, q := range quotes {
		if dateOf(q.TradeDate).Before(floor) || !q.AsOf.Before(cutoff) {
			continue
		}
		if cur, ok := latest[q.Code]; !ok || q.TradeDate.After(cur.TradeDate) {
			latest[q.Code] = q
		}
	}
	out := map[string]float64{}
	for _, m := range mappings {
		q, ok := latest[m.Asset]
		if !ok {
			continue
		}
		out[m.Sector] += m.Weight * q.PctChg
	}
	return out
}
