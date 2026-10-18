package biz

import (
	"context"
	"fmt"
	"sort"
	"time"

	"server/pkg/accountstate"
	"server/pkg/risk"
	"server/pkg/tradecal"

	"github.com/google/uuid"
)

func (e *Engine) accountSnapshot(ctx context.Context, book string) (AccountView, Cash, accountstate.Snapshot, error) {
	cash, err := e.cash(ctx, book)
	if err != nil {
		return AccountView{}, cash, accountstate.Snapshot{}, err
	}
	positions, err := e.store.ListPositions(ctx, book)
	if err != nil {
		return AccountView{}, cash, accountstate.Snapshot{}, err
	}
	// Negative limit is the explicit unbounded ledger read, not the UI's last 100.
	orders, err := e.store.ListOrders(ctx, book, -1)
	if err != nil {
		return AccountView{}, cash, accountstate.Snapshot{}, err
	}
	now := e.clock()
	s := accountstate.Snapshot{
		Version: accountstate.Version, Kind: "full", AccountID: accountstate.DefaultAccountID,
		AccountType: risk.AccountType(accountOf(book)), SnapshotID: uuid.NewString(), AsOf: now,
		TradingDay: now.In(tradecal.Shanghai()).Format("2006-01-02"), Cash: cash.Cash,
		Holdings: []risk.Holding{}, OpenOrders: []risk.OpenOrder{}, ObservedOrders: []string{},
		Reconciliation: accountstate.Reconciliation{Status: "matched", AsOf: now},
	}
	if book == BookLive {
		// There is no connected broker ledger yet. Never label synthetic initial
		// cash or a local position comparison as a reconciled LIVE account.
		s.Reconciliation.Status, s.Reconciliation.Reason = "unavailable", "实盘柜台账户及对账尚未接入"
	}
	if cash.OpenHalted {
		s.Reconciliation.Status, s.Reconciliation.Reason = "blocked", cash.HaltReason
	}
	s.DayStartKnown = cash.DayStartDate == s.TradingDay && cash.DayStartEquity > 0
	if s.DayStartKnown {
		s.DayStartEquity = cash.DayStartEquity
	}
	view := AccountView{Book: book, Cash: cash.Cash, RealizedPnL: cash.RealizedPnL, PaperDays: cash.PaperDays, OpenHalted: cash.OpenHalted, HaltReason: cash.HaltReason}
	frozenSells := map[string]int{}
	for _, o := range orders {
		s.ObservedOrders = append(s.ObservedOrders, o.ClientOrderID)
		if !reserving(o) {
			continue
		}
		remaining := o.Volume - o.Filled
		if remaining <= 0 {
			continue
		}
		s.OpenOrders = append(s.OpenOrders, risk.OpenOrder{ClientID: o.ClientOrderID, Symbol: o.Symbol, Side: risk.Side(o.Side), Price: o.Price, Volume: remaining})
		if o.Side == "buy" {
			s.FrozenCash += e.frozenBuy(o)
		} else {
			frozenSells[o.Symbol] += remaining
		}
	}
	sort.Strings(s.ObservedOrders)
	sort.Slice(s.OpenOrders, func(i, j int) bool { return s.OpenOrders[i].ClientID < s.OpenOrders[j].ClientID })
	for _, p := range positions {
		if p.Quantity <= 0 {
			continue
		}
		px := p.LastPrice
		if px <= 0 {
			px = p.AvgCost
		}
		s.Holdings = append(s.Holdings, risk.Holding{Symbol: p.Symbol, Quantity: p.Quantity, Available: p.Available, FrozenSell: frozenSells[p.Symbol], AvgCost: p.AvgCost, MarketValue: px * float64(p.Quantity)})
		view.Equity += px * float64(p.Quantity)
		view.UnrealizedPnL += (px - p.AvgCost) * float64(p.Quantity)
		view.PositionCount++
	}
	view.Equity += cash.Cash
	s.Equity, s.Available = view.Equity, cash.Cash-s.FrozenCash
	// Do not clamp a broken reserve into a valid account: persistence/consumer
	// validation must reject the complete snapshot and keep the last valid one.
	return view, cash, s, nil
}

func reserving(o Order) bool {
	switch o.Status {
	case StatusPending, StatusSubmitted, StatusPartial, "submitting", "cancel_pending", "unknown":
		return true
	default:
		return false
	}
}

func (e *Engine) frozenBuy(o Order) float64 {
	amount := o.Price * float64(o.Volume-o.Filled)
	return amount + e.set.Fee.Cost(amount, false)
}

func (e *Engine) reserved(ctx context.Context, book, exclude string, overlay []Order) (float64, map[string]int, error) {
	orders, err := e.store.ListOrders(ctx, book, -1)
	if err != nil {
		return 0, nil, err
	}
	byID := make(map[string]Order, len(orders)+len(overlay))
	for _, o := range orders {
		byID[o.ClientOrderID] = o
	}
	for _, o := range overlay {
		byID[o.ClientOrderID] = o
	}
	cash, sells := 0.0, map[string]int{}
	for _, o := range byID {
		if o.ClientOrderID == exclude || !reserving(o) || o.Volume <= o.Filled {
			continue
		}
		if o.Side == "buy" {
			cash += e.frozenBuy(o)
		} else {
			sells[o.Symbol] += o.Volume - o.Filled
		}
	}
	return cash, sells, nil
}

// startDay sets a baseline only at an explicit day transition. Loading an old
// ledger during the session must not turn current equity into a fabricated start.
func (e *Engine) startDay(cash *Cash, positions []Position, at time.Time) {
	cash.DayStartDate = at.In(tradecal.Shanghai()).Format("2006-01-02")
	cash.DayStartEquity = cash.Cash
	for _, p := range positions {
		px := p.LastPrice
		if px <= 0 {
			px = p.AvgCost
		}
		cash.DayStartEquity += px * float64(p.Quantity)
	}
}

func (e *Engine) snapshot(ctx context.Context, book string) (AccountView, error) {
	if err := e.prepareDay(ctx, book, false, false); err != nil {
		return AccountView{}, err
	}
	view, cash, s, err := e.accountSnapshot(ctx, book)
	if err != nil {
		return AccountView{}, err
	}
	if _, err := e.store.CommitSnapshot(ctx, book, view, cash, s); err != nil {
		return AccountView{}, fmt.Errorf("account snapshot: %w", err)
	}
	return view, nil
}

// prepareDay runs under Engine.mu before any matching or marking can change
// yesterday's ledger. A new trading day's baseline and T+1 settlement are one
// ledger transaction. Legacy rows with no baseline remain explicitly unknown.
// The old paper-day counter is not continuous-run admission evidence (N7-03).
func (e *Engine) prepareDay(ctx context.Context, book string, strict, countManualDay bool) error {
	cash, err := e.cash(ctx, book)
	if err != nil {
		return err
	}
	day := e.clock().In(tradecal.Shanghai()).Format("2006-01-02")
	if cash.DayStartDate == day {
		return nil
	}
	if cash.DayStartDate == "" {
		if strict {
			return fmt.Errorf("%w: 缺少可信日初权益，账户账期待恢复", ErrInvalid)
		}
		return nil
	}
	if _, err := time.ParseInLocation("2006-01-02", cash.DayStartDate, tradecal.Shanghai()); err != nil {
		return fmt.Errorf("%w: 非法账户账期日期", ErrInvalid)
	}
	if cash.DayStartDate > day {
		return fmt.Errorf("%w: 账户账期晚于当前时间", ErrInvalid)
	}
	open, err := tradecal.Default.Open(e.clock())
	if err != nil || !open {
		if strict {
			return fmt.Errorf("%w: 当前不是可结转的交易日", ErrInvalid)
		}
		return nil
	}
	fills, err := e.store.ListFills(ctx, book, -1)
	if err != nil {
		return err
	}
	for _, fill := range fills {
		if fill.Qty > 0 && fill.CreatedAt.In(tradecal.Shanghai()).Format("2006-01-02") == day {
			return fmt.Errorf("%w: 当日已有成交但日初权益未锁定，必须先核对账户账期", ErrInvalid)
		}
	}
	positions, err := e.store.ListPositions(ctx, book)
	if err != nil {
		return err
	}
	e.startDay(&cash, positions, e.clock())
	for i := range positions {
		positions[i].Available = positions[i].Quantity
	}
	if book == BookPaper && countManualDay {
		cash.PaperDays++
	}
	return e.store.Commit(ctx, Batch{Cash: &cash, Positions: positions})
}
