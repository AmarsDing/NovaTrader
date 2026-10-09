package rules

import "fmt"

// Exit 是两个模板共用的卖出判断，按 止损 → 突发利空 → 止盈 → 情绪退潮 → 时间到 的顺序。
// 只在盘中（Today 不为空）且有可卖股数时给出卖出计划，股数为全部可卖。
// holdMax 为 0 时不做时间止损；持仓的止损、止盈为 0 时跳过对应判断。
func Exit(s Snapshot, pos Position, holdMax int, price PriceParams) (Plan, bool) {
	if s.Today == nil || pos.Available <= 0 {
		return Plan{}, false
	}
	last := s.Last()
	kind, reason := "", ""
	switch {
	case pos.StopLoss > 0 && last <= pos.StopLoss+eps:
		kind, reason = ExitStopLoss, fmt.Sprintf("现价 %.2f 触及止损 %.2f", last, pos.StopLoss)
	case s.NegativeNews:
		kind, reason = ExitBadNews, "突发利空"
	case pos.TakeProfit > 0 && last >= pos.TakeProfit-eps:
		kind, reason = ExitTakeProfit, fmt.Sprintf("现价 %.2f 触及止盈 %.2f", last, pos.TakeProfit)
	case s.Stage == StageFade:
		kind, reason = ExitSentiment, "情绪退潮"
	case holdMax > 0 && pos.HoldDays >= holdMax:
		kind, reason = ExitTime, fmt.Sprintf("已持有 %d 个交易日，上限 %d", pos.HoldDays, holdMax)
	default:
		return Plan{}, false
	}
	px, err := price.SellPrice(s)
	if err != nil || px <= 0 {
		return Plan{}, false
	}
	return Plan{Side: SideSell, Price: px, Shares: pos.Available, Reason: reason, ExitKind: kind}, true
}
