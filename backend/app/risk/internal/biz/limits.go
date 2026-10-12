package biz

import (
	"strings"

	"server/pkg/risk"
)

// LimitItem 一条限额的上限与当前用量（监控驾驶舱 P1，只读）。
type LimitItem struct {
	Key   string
	Name  string
	Unit  string
	Limit float64 // -1 不适用
	Used  float64 // -1 不可得
	Ratio float64 // 任一为负则 -1
	Note  string
}

func ratio(used, limit float64) float64 {
	if used < 0 || limit <= 0 {
		return -1
	}
	return used / limit
}

// Limits 汇总各项限额的上限与当前用量。accountType 空为两本账合计。
// 用量全部来自引擎内存（账户快照、当日放行计数、速度计数），不查库。
func (e *Engine) Limits(accountType string) []LimitItem {
	e.mu.RLock()
	defer e.mu.RUnlock()

	p := e.params
	scale := p.Scale(e.market)
	now := e.now()

	// 选定账本集合
	types := []risk.AccountType{risk.SIM, risk.LIVE}
	if t := risk.AccountType(strings.ToUpper(strings.TrimSpace(accountType))); t == risk.SIM || t == risk.LIVE {
		types = []risk.AccountType{t}
	}

	var equity, usedTotal, usedSingleMax float64
	var buysToday, lastSecond, ordersToday int
	sectorUsed := map[string]float64{}
	for _, t := range types {
		a, ok := e.accounts[t]
		if !ok {
			continue
		}
		equity += a.Equity
		buysToday += e.buys[t]
		if r := e.rates[t]; r != nil {
			ls, td := r.Counts(now)
			lastSecond += ls
			ordersToday += td
		}
		for _, h := range a.Holdings {
			usedTotal += h.MarketValue
			if h.MarketValue > usedSingleMax {
				usedSingleMax = h.MarketValue
			}
			if h.Sector != "" {
				sectorUsed[h.Sector] += h.MarketValue
			}
		}
	}
	// 在途买单与未落地预约也占额度（与 ruleTotal 同口径）
	for _, t := range types {
		if a, ok := e.accounts[t]; ok {
			for _, o := range a.Open {
				if o.Side == risk.Buy {
					usedTotal += o.Price * float64(o.Volume)
				}
			}
		}
	}
	for _, res := range e.reservations {
		usedTotal += res.Amount
	}
	usedSectorMax := 0.0
	for _, v := range sectorUsed {
		if v > usedSectorMax {
			usedSectorMax = v
		}
	}

	// 当日亏损用量：合计权益 vs 合计开盘权益
	var dayStart float64
	for _, t := range types {
		if a, ok := e.accounts[t]; ok {
			dayStart += a.DayStartEquity
		}
	}
	lossUsed := -1.0
	if dayStart > 0 && equity > 0 {
		lossUsed = (dayStart - equity) / dayStart // 负值表示当日是赚的
	}

	items := []LimitItem{
		{Key: "risk.max_daily_buys", Name: "当日开仓笔数", Unit: "笔",
			Limit: float64(p.MaxDailyBuys), Used: float64(buysToday),
			Note: "R10 当日开仓笔数"},
		{Key: "risk.max_single_pct", Name: "单票仓位", Unit: "元",
			Limit: equity * p.MaxSinglePct * scale, Used: usedSingleMax,
			Note: "R07 单票仓位（含情绪系数）；金额为市值口径"},
		{Key: "risk.max_total_pct", Name: "总仓", Unit: "元",
			Limit: equity * p.MaxTotalPct * scale, Used: usedTotal,
			Note: "R08 总仓（持仓市值 + 在途买单 + 未落地预约，含情绪系数）"},
		{Key: "risk.max_sector_pct", Name: "板块集中", Unit: "元",
			Limit: equity * p.MaxSectorPct, Used: usedSectorMax,
			Note: "R09 板块集中（最大单板块市值）"},
		{Key: "risk.daily_loss_pct", Name: "当日亏损", Unit: "%",
			Limit: p.DailyLossPct, Used: lossUsed,
			Note: "R15 熔断·当日亏损（合计权益口径；负值表示当日盈利）"},
		{Key: "risk.max_orders_per_sec", Name: "每秒下单", Unit: "笔每秒",
			Limit: float64(p.MaxOrdersPerSec), Used: float64(lastSecond),
			Note: "R14 速度上限·每秒"},
		{Key: "risk.max_orders_per_day", Name: "当日下单", Unit: "笔",
			Limit: float64(p.MaxOrdersPerDay), Used: float64(ordersToday),
			Note: "R14 速度上限·当日"},
	}
	for i := range items {
		items[i].Ratio = ratio(items[i].Used, items[i].Limit)
	}
	return items
}
