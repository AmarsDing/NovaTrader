package sim

import (
	"math"

	"server/pkg/ashare"
	"server/pkg/brokerif"
	"server/pkg/symbol"
)

// LimitFill 决定买单成交价落在涨停价（卖单落在跌停价）时怎么处理。
const (
	// LimitFillOpened：这根 K 线打开过（买：最低价低于涨停价），就按成交价成交。
	LimitFillOpened = "opened"
	// LimitFillNever：成交价等于涨停价（跌停价）就不成交，不假设能排到队。
	LimitFillNever = "never"
)

// MatchRule 是撮合参数。回测和模拟盘读同一份配置。
type MatchRule struct {
	SlippageTicks int
	VolumeCap     float64
	LimitFill     string
}

// DefaultMatchRule 滑点 1 个最小价位，单笔不超过该 K 线成交量的 10%。
func DefaultMatchRule() MatchRule {
	return MatchRule{SlippageTicks: 1, VolumeCap: 0.1, LimitFill: LimitFillOpened}
}

// Quote 是用来撮合的那一根 K 线和当日涨跌停价，全部不复权。
type Quote struct {
	Open, High, Low float64
	Volume          int64
	LimitUp         float64
	LimitDown       float64
}

// Fill 是撮合结果。Qty 为 0 时 Reason 写明为什么不成交。
type Fill struct {
	Qty    int
	Price  float64
	Reason string
}

// Match 用一根 K 线撮合一张委托。limit 为 0 表示不限价。available 只对卖单有意义，用来判断能否一次卖完余股。
func Match(sym string, side brokerif.Side, qty, available int, limit float64, q Quote, r MatchRule) Fill {
	if qty <= 0 {
		return Fill{Reason: "数量为 0"}
	}
	if q.Volume <= 0 || q.Open <= 0 {
		return Fill{Reason: "停牌或无成交"}
	}
	tick := float64(r.SlippageTicks) * ashare.Tick
	var price float64
	switch side {
	case brokerif.Buy:
		if q.LimitUp > 0 && q.Low >= q.LimitUp {
			return Fill{Reason: "涨停封板"}
		}
		price = ashare.Clamp(q.Open+tick, q.LimitUp, q.LimitDown)
		if r.LimitFill == LimitFillNever && q.LimitUp > 0 && price >= q.LimitUp {
			return Fill{Reason: "涨停不排队"}
		}
		if limit > 0 && price > limit+1e-9 {
			return Fill{Reason: "价格未到"}
		}
	case brokerif.Sell:
		if q.LimitDown > 0 && q.High <= q.LimitDown {
			return Fill{Reason: "跌停封板"}
		}
		price = ashare.Clamp(q.Open-tick, q.LimitUp, q.LimitDown)
		if r.LimitFill == LimitFillNever && q.LimitDown > 0 && price <= q.LimitDown {
			return Fill{Reason: "跌停不排队"}
		}
		if limit > 0 && price < limit-1e-9 {
			return Fill{Reason: "价格未到"}
		}
	default:
		return Fill{Reason: "未知方向"}
	}
	n := qty
	if r.VolumeCap > 0 {
		capQty := int(math.Floor(float64(q.Volume) * r.VolumeCap))
		if capQty < n {
			n = capQty
		}
	}
	n = roundLot(sym, side, n, qty, available)
	if n > 0 && side == brokerif.Sell {
		if ok, _ := ashare.CanSell(sym, available, n); !ok {
			n = 0
		}
	}
	if n <= 0 {
		return Fill{Reason: "量能不足"}
	}
	return Fill{Qty: n, Price: price}
}

// roundLot 把成交量上限截断后的数量取整到合法手数。
func roundLot(raw string, side brokerif.Side, n, want, available int) int {
	if n <= 0 {
		return 0
	}
	if side == brokerif.Buy {
		v, err := ashare.RoundBuy(raw, n)
		if err != nil {
			return 0
		}
		return v
	}
	sym, err := symbol.Parse(raw)
	if err != nil {
		return 0
	}
	if ashare.IsSTAR(sym.Code) || (n == want && n == available) {
		return n
	}
	return n / 100 * 100
}
