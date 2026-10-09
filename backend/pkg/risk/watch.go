package risk

import (
	"fmt"
	"math"

	"server/pkg/ashare"
)

// 盯盘触发类型，按优先级从高到低。
const (
	ExitStopLoss    = "stop_loss"
	ExitIndexPlunge = "index_plunge"
	ExitBadNews     = "bad_news"
	ExitTrailing    = "trailing_stop"
	ExitTakeProfit  = "take_profit"
	ExitTimeStop    = "time_stop"
)

// WatchInput 是一只持仓在某一时刻的盯盘输入。High 是持仓以来见过的最高价。
type WatchInput struct {
	Input
	Holding      Holding
	High         float64
	NegativeNews bool
}

// Exit 是盯盘给出的卖出建议，由 trade 生成卖单后仍走 Check。
type Exit struct {
	Symbol  string      `json:"symbol"`
	Account AccountType `json:"account_type"`
	Trigger string      `json:"trigger"`
	Volume  int         `json:"volume"`
	Price   float64     `json:"price"`
	Note    string      `json:"note"`
}

// WatchPosition 检查一只持仓是否该卖。只在连续交易时段、有可卖数量时触发。
func WatchPosition(w WatchInput, p Params) (Exit, bool) {
	h, q := w.Holding, w.Quote
	if !w.Session.Trading || q == nil || q.Last <= 0 || q.Suspended || h.AvgCost <= 0 {
		return Exit{}, false
	}
	vol := h.Sellable()
	if vol <= 0 {
		return Exit{}, false
	}
	last := q.Last
	high := math.Max(w.High, math.Max(last, h.AvgCost))
	gain := (last - h.AvgCost) / h.AvgCost
	stop := h.StopPrice
	if stop <= 0 {
		stop = h.AvgCost * (1 - p.StopPct)
	}

	trigger, note := "", ""
	switch {
	case last <= stop+1e-9:
		trigger, note = ExitStopLoss, fmt.Sprintf("现价 %.2f ≤ 止损 %.2f", last, stop)
	case w.Market.Index5mChange <= p.IndexPlunge5m:
		trigger, note = ExitIndexPlunge, fmt.Sprintf("指数 5 分钟跌 %.2f%%", -w.Market.Index5mChange*100)
	case w.NegativeNews:
		trigger, note = ExitBadNews, "突发利空"
	case (high-h.AvgCost)/h.AvgCost >= p.TrailStartPct && (high-last)/high >= p.TrailDrawdownPct:
		trigger, note = ExitTrailing, fmt.Sprintf("最高 %.2f 回撤到 %.2f", high, last)
	case h.TakePrice > 0 && last >= h.TakePrice-1e-9:
		trigger, note = ExitTakeProfit, fmt.Sprintf("现价 %.2f ≥ 止盈 %.2f", last, h.TakePrice)
	case w.Session.Late && h.HeldDays >= p.TimeStopDays && gain < p.TimeStopMinGain:
		trigger, note = ExitTimeStop, fmt.Sprintf("持有 %d 日，浮盈 %.2f%%", h.HeldDays, gain*100)
	default:
		return Exit{}, false
	}
	w.Order = Order{Symbol: h.Symbol, Side: Sell, Account: w.Account.Type, Source: Watch}
	return Exit{
		Symbol:  h.Symbol,
		Account: w.Account.Type,
		Trigger: trigger,
		Volume:  vol,
		Price:   ExitPrice(&w.Input, p),
		Note:    note,
	}, true
}

// ExitPrice 是盯盘卖出的限价：买一减 N 个价位，不低于价格笼子下限和跌停价。没有买一时挂跌停价。
func ExitPrice(in *Input, p Params) float64 {
	q := in.Quote
	floor := 0.0
	_, down, limited, err := LimitPrices(in)
	if err == nil && limited {
		floor = down
	}
	if Continuous(in) {
		bench := ashare.CageBenchmark(true, q.Bid1, q.Ask1, q.Last, q.PrevClose)
		if cage, err := ashare.Cage(in.Order.Symbol, true, bench); err == nil && cage > floor {
			floor = cage
		}
	}
	if q.Bid1 <= 0 {
		if floor > 0 {
			return floor
		}
		return q.Last
	}
	price := math.Round((q.Bid1-float64(p.ExitSlipTicks)*ashare.Tick)*100) / 100
	if price < floor {
		price = floor
	}
	return price
}
