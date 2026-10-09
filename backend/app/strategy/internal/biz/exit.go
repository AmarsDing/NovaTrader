package biz

import (
	"context"
	"fmt"
	"time"

	"server/pkg/events"
	"server/pkg/rules"

	"github.com/google/uuid"
)

// ScanExits 检查可卖持仓，按开仓模板的 ExitPlan 出卖出信号。Kill Switch 不影响卖出。
func (uc *Usecase) ScanExits(ctx context.Context, asOf time.Time) ([]int, error) {
	uc.scanMu.Lock()
	defer uc.scanMu.Unlock()
	if asOf.IsZero() {
		asOf = time.Now()
	}
	trace := events.TraceID(ctx)
	if trace == "" {
		trace = uuid.NewString()
	}
	holdings, err := uc.positions.Holdings(ctx, uc.cfg.Book)
	if err != nil {
		return nil, fmt.Errorf("strategy: positions: %w", err)
	}
	byCode := map[string]Holding{}
	var symbols []string
	for _, h := range holdings {
		if h.Available > 0 {
			byCode[h.Symbol] = h
			symbols = append(symbols, h.Symbol)
		}
	}
	if len(symbols) == 0 {
		return nil, nil
	}
	snaps, err := uc.market.Snapshots(ctx, symbols, asOf)
	if err != nil {
		return nil, fmt.Errorf("strategy: snapshots: %w", err)
	}
	p := uc.loadParams(ctx)
	day := tradeDay(asOf)
	var ids []int
	for _, s := range snaps {
		h, ok := byCode[s.Symbol]
		if !ok {
			continue
		}
		buy, err := uc.signals.LastDoneBuy(ctx, uc.cfg.Book, s.Symbol)
		if err != nil {
			return ids, err
		}
		pos := rules.Position{Symbol: h.Symbol, Quantity: h.Quantity, Available: h.Available, AvgCost: h.AvgCost}
		var plan rules.Plan
		opened := ""
		if buy != nil {
			pos.StopLoss, pos.TakeProfit, opened = buy.StopLoss, buy.TakeProfit, buy.Strategy
			pos.HoldDays = uc.holdDays(buy.TradeDate, day)
		}
		if st := rules.ByName(p.Strategies, opened); st != nil {
			plan, ok = st.ExitPlan(ctx, s, pos)
		} else {
			plan, ok = rules.Exit(s, pos, 0, p.Price)
		}
		if !ok {
			continue
		}
		cooled, err := uc.cooled(ctx, s.Symbol, rules.SideSell, asOf, p)
		if err != nil {
			return ids, err
		}
		if cooled {
			continue
		}
		sig := &Signal{
			Book: uc.cfg.Book, Time: asOf, TradeDate: day, Symbol: s.Symbol, Name: s.Name,
			Side: rules.SideSell, Strategy: opened, VersionID: p.VersionID,
			Entry: plan.Price, EntryLow: plan.Price, EntryHigh: plan.Price,
			StopLoss: pos.StopLoss, TakeProfit: pos.TakeProfit,
			PositionPct: 1, ValidUntil: validUntil(asOf, p.TTL),
			ExitKind: plan.ExitKind, Reason: plan.Reason, Status: StatusPending, TraceID: trace,
		}
		id, err := uc.signals.Create(ctx, sig, signalEnvelope)
		if err != nil {
			return ids, fmt.Errorf("strategy: create sell signal: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// holdDays 是买入日之后到 day（含）经过的交易日数。日历缺年份时按工作日估算。
func (uc *Usecase) holdDays(bought, day time.Time) int {
	if bought.IsZero() {
		return 0
	}
	n := 0
	for d := tradeDay(bought).AddDate(0, 0, 1); !d.After(day); d = d.AddDate(0, 0, 1) {
		open, err := uc.cal.Open(d)
		if err != nil {
			open = d.Weekday() != time.Saturday && d.Weekday() != time.Sunday
		}
		if open {
			n++
		}
	}
	return n
}
