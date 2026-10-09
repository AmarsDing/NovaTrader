package biz

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"server/pkg/ashare"
	"server/pkg/brokerif"
	"server/pkg/brokerif/sim"
	"server/pkg/events"
	"server/pkg/symbol"

	"github.com/go-kratos/kratos/v2/log"
)

// Settings 是模拟撮合和实盘门槛。
type Settings struct {
	InitialCash       float64
	PaperDaysRequired int
	Fee               ashare.Fee
	Match             sim.MatchRule
	LiveEnabled       bool
	// WorkTimeout 是盯盘限价单未成交多久后撤掉剩余。默认 60 秒。
	WorkTimeout time.Duration
	// ChaseMax 是超时后最多重报几次。默认 2。
	ChaseMax int
	// ChaseCap 是相对第一张单价格的追价幅度，0.005 表示 0.5%。
	ChaseCap float64
}

func (s Settings) norm() Settings {
	if s.InitialCash <= 0 {
		s.InitialCash = 1_000_000
	}
	if s.PaperDaysRequired <= 0 {
		s.PaperDaysRequired = 20
	}
	if s.Fee == (ashare.Fee{}) {
		s.Fee = ashare.DefaultFee()
	}
	if s.Match.VolumeCap == 0 && s.Match.SlippageTicks == 0 && s.Match.LimitFill == "" {
		s.Match = sim.DefaultMatchRule()
	}
	if s.WorkTimeout <= 0 {
		s.WorkTimeout = 60 * time.Second
	}
	if s.ChaseMax <= 0 {
		s.ChaseMax = 2
	}
	if s.ChaseCap <= 0 {
		s.ChaseCap = 0.005
	}
	return s
}

// Engine 串行处理一个进程内的委托。每次变化一次 Commit。
type Engine struct {
	store Store
	risk  Checker
	set   Settings
	mu    sync.Mutex
	now   func() time.Time
	log   *log.Helper
}

func NewEngine(store Store, risk Checker, set Settings, logger log.Logger) *Engine {
	return &Engine{store: store, risk: risk, set: set.norm(), log: log.NewHelper(logger)}
}

func (e *Engine) Place(ctx context.Context, req PlaceRequest) (Order, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.place(ctx, req)
}

func (e *Engine) place(ctx context.Context, req PlaceRequest) (Order, error) {
	req.ClientOrderID = strings.TrimSpace(req.ClientOrderID)
	req.Symbol = strings.TrimSpace(req.Symbol)
	req.Side = strings.ToLower(strings.TrimSpace(req.Side))
	req.Source = strings.ToLower(strings.TrimSpace(req.Source))
	if req.Source == "" {
		req.Source = "auto"
	}
	if req.ClientOrderID == "" || req.Price <= 0 || req.Volume <= 0 {
		return Order{}, fmt.Errorf("%w: 委托字段不完整", ErrInvalid)
	}
	if _, err := symbol.Parse(req.Symbol); err != nil {
		return Order{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if req.Side != string(brokerif.Buy) && req.Side != string(brokerif.Sell) {
		return Order{}, fmt.Errorf("%w: 方向只能是 buy 或 sell", ErrInvalid)
	}
	book, err := bookOf(req.Account)
	if err != nil {
		return Order{}, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	if existing, ok, err := e.store.LoadOrder(ctx, req.ClientOrderID); err != nil {
		return Order{}, err
	} else if ok {
		return existing, nil
	}
	if req.Source == "manual" && strings.TrimSpace(req.Operator) == "" {
		return Order{}, fmt.Errorf("%w: 手动下单必须填写操作人", ErrInvalid)
	}

	cash, err := e.cash(ctx, book)
	if err != nil {
		return Order{}, err
	}
	order := Order{
		ClientOrderID: req.ClientOrderID, Book: book, Symbol: req.Symbol, Side: req.Side,
		Status: StatusSubmitted, Price: req.Price, Volume: req.Volume, Source: req.Source,
		Operator: req.Operator, SignalID: req.SignalID, StrategyVersion: req.StrategyVersion,
		Channel: ChannelSim, CreatedAt: e.clock(),
	}
	var trails []Trail

	reject := func(reason string) (Order, error) {
		order.Status = StatusRejected
		order.Reason = reason
		trails = append(trails, Trail{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: reason})
		if err := e.store.Commit(ctx, Batch{
			Orders: []Order{order},
			Trails: trails,
			Events: []Event{{Subject: events.SubjectTradeOrder, Payload: orderPayload(order)}},
		}); err != nil {
			return Order{}, err
		}
		return order, nil
	}

	if book == BookLive {
		paper, err := e.cash(ctx, BookPaper)
		if err != nil {
			return Order{}, err
		}
		if !e.set.LiveEnabled || paper.PaperDays < e.set.PaperDaysRequired {
			return reject("模拟盘未满准入交易日，或实盘通道未打开")
		}
	}
	if req.Side == string(brokerif.Buy) && cash.OpenHalted {
		return reject("对账不平，已停止开仓")
	}
	if req.Side == string(brokerif.Buy) {
		n, err := ashare.RoundBuy(req.Symbol, req.Volume)
		if err != nil || n != req.Volume {
			return reject("买入数量不是合法手数")
		}
	}

	out, err := e.risk.Check(ctx, CheckIn{
		ClientOrderID: req.ClientOrderID, Account: accountOf(book), Symbol: req.Symbol,
		Side: req.Side, Price: req.Price, Volume: req.Volume, Source: req.Source, Operator: req.Operator,
	})
	if err != nil {
		return reject("风控不可用")
	}
	if !out.Approved {
		reason := out.Reason
		if reason == "" {
			reason = "风控拒绝"
		}
		return reject(reason)
	}
	if out.Volume > 0 && out.Volume < order.Volume {
		order.Volume = out.Volume
		if req.Side == string(brokerif.Buy) {
			n, err := ashare.RoundBuy(req.Symbol, order.Volume)
			if err != nil || n != order.Volume {
				return reject("风控缩量后不是合法手数")
			}
		}
	}

	pos, _, err := e.store.LoadPosition(ctx, book, req.Symbol)
	if err != nil {
		return Order{}, err
	}
	pos.Book = book
	pos.Symbol = req.Symbol
	if req.Side == string(brokerif.Sell) {
		ok, err := ashare.CanSell(req.Symbol, pos.Available, order.Volume)
		if err != nil || !ok {
			return reject("可卖数量不足（含 T+1）")
		}
	}

	// 盯盘卖出是保护性退出，不等 30 秒确认窗。开仓和手动单仍要确认。
	if book == BookLive && req.Source != "watch" {
		order.Status = StatusPending
		order.Reason = "等待确认"
		if err := e.store.Commit(ctx, Batch{
			Orders: []Order{order},
			Trails: []Trail{{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: order.Reason}},
			Events: []Event{
				{Subject: events.SubjectTradeOrder, Payload: orderPayload(order)},
				{Subject: events.SubjectNotifyDesktop, Payload: confirmPayload(order)},
			},
		}); err != nil {
			return Order{}, err
		}
		return order, nil
	}

	trails = append(trails, Trail{ClientOrderID: order.ClientOrderID, Status: StatusSubmitted, Note: "已接收"})
	batch := Batch{Orders: []Order{order}, Trails: trails, Cash: &cash}
	if req.Quote != nil {
		e.fill(ctx, &batch, &order, pos, *req.Quote)
		batch.Orders = []Order{order}
	}
	batch.Events = append(batch.Events, Event{Subject: events.SubjectTradeOrder, Payload: orderPayload(order)})
	if err := e.store.Commit(ctx, batch); err != nil {
		return Order{}, err
	}
	return order, nil
}

func (e *Engine) fill(ctx context.Context, batch *Batch, order *Order, pos Position, q Quote) {
	if order.Status != StatusSubmitted && order.Status != StatusPartial {
		return
	}
	remain := order.Volume - order.Filled
	side := brokerif.Side(order.Side)
	got := sim.Match(order.Symbol, side, remain, pos.Available, order.Price, sim.Quote{
		Open: q.Open, High: q.High, Low: q.Low, Volume: q.Volume,
		LimitUp: q.LimitUp, LimitDown: q.LimitDown,
	}, e.set.Match)
	if got.Qty <= 0 {
		order.Reason = got.Reason
		return
	}
	cash := *batch.Cash
	qty := got.Qty
	if side == brokerif.Buy {
		qty = afford(e.set.Fee, cash.Cash, order.Symbol, qty, got.Price)
		if qty <= 0 {
			order.Reason = "资金不足"
			return
		}
	}
	amount := got.Price * float64(qty)
	fee := e.set.Fee.Cost(amount, side == brokerif.Sell)
	if side == brokerif.Buy {
		cash.Cash -= amount + fee
		prev := pos.Quantity
		pos.Quantity += qty
		if pos.Quantity > 0 {
			pos.AvgCost = (pos.AvgCost*float64(prev) + got.Price*float64(qty)) / float64(pos.Quantity)
		}
		pos.Available = ashare.Sellable(pos.Quantity, pos.Quantity-pos.Available)
	} else {
		cash.Cash += amount - fee
		cash.RealizedPnL += (got.Price-pos.AvgCost)*float64(qty) - fee
		pos.Quantity -= qty
		pos.Available -= qty
		if pos.Available < 0 {
			pos.Available = 0
		}
	}
	if q.Close > 0 {
		pos.LastPrice = q.Close
	} else {
		pos.LastPrice = got.Price
	}
	order.Filled += qty
	if order.Filled >= order.Volume {
		order.Status = StatusFilled
		order.Reason = ""
	} else {
		order.Status = StatusPartial
		order.Reason = "部分成交"
	}
	*batch.Cash = cash
	batch.Positions = upsertPos(batch.Positions, pos)
	fill := Fill{
		ClientOrderID: order.ClientOrderID, Book: order.Book, Symbol: order.Symbol, Side: order.Side,
		Qty: qty, Price: got.Price, Amount: amount, Fee: fee,
		SignalID: order.SignalID, StrategyVersion: order.StrategyVersion, CreatedAt: e.clock(),
	}
	batch.Fills = append(batch.Fills, fill)
	batch.Trails = append(batch.Trails, Trail{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: order.Reason})
	batch.Events = append(batch.Events, Event{Subject: events.SubjectTradeFill, Payload: fill})
	_ = ctx
}

func afford(fee ashare.Fee, cash float64, sym string, want int, price float64) int {
	acc := sim.NewAccount(cash, fee)
	return acc.Affordable(sym, want, price)
}

func upsertPos(list []Position, pos Position) []Position {
	for i := range list {
		if list[i].Symbol == pos.Symbol && list[i].Book == pos.Book {
			list[i] = pos
			return list
		}
	}
	return append(list, pos)
}

func (e *Engine) ApplyBar(ctx context.Context, account, rawSymbol string, q Quote) ([]Order, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	book, err := bookOf(account)
	if err != nil {
		return nil, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	sym := strings.TrimSpace(rawSymbol)
	orders, err := e.store.ListOpen(ctx, book, sym)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, nil
	}
	cash, err := e.cash(ctx, book)
	if err != nil {
		return nil, err
	}
	batch := Batch{Cash: &cash}
	var out []Order
	for i := range orders {
		pos, _, err := e.store.LoadPosition(ctx, book, orders[i].Symbol)
		if err != nil {
			return nil, err
		}
		pos = overlay(batch.Positions, pos)
		pos.Book = book
		pos.Symbol = orders[i].Symbol
		if q.Close > 0 {
			pos.LastPrice = q.Close
		}
		e.fill(ctx, &batch, &orders[i], pos, q)
		batch.Orders = append(batch.Orders, orders[i])
		out = append(out, orders[i])
	}
	if q.Close > 0 {
		if pos, ok, err := e.store.LoadPosition(ctx, book, sym); err != nil {
			return nil, err
		} else if ok {
			pos.LastPrice = q.Close
			batch.Positions = upsertPos(batch.Positions, pos)
		}
	}
	batch.Events = append(batch.Events, Event{Subject: events.SubjectTradeOrder, Payload: map[string]any{"book": book, "symbol": sym, "count": len(out)}})
	if err := e.store.Commit(ctx, batch); err != nil {
		return nil, err
	}
	return out, nil
}

func overlay(list []Position, pos Position) Position {
	for _, p := range list {
		if p.Symbol == pos.Symbol && p.Book == pos.Book {
			return p
		}
	}
	return pos
}

func (e *Engine) Cancel(ctx context.Context, clientID string) (Order, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	order, ok, err := e.store.LoadOrder(ctx, strings.TrimSpace(clientID))
	if err != nil {
		return Order{}, err
	}
	if !ok {
		return Order{}, ErrNotFound
	}
	if order.Status == StatusPending {
		return e.void(ctx, order, "确认已放弃")
	}
	if order.Status != StatusSubmitted && order.Status != StatusPartial {
		return Order{}, fmt.Errorf("%w: 当前状态 %s 不能撤单", ErrInvalid, order.Status)
	}
	order.Status = StatusCancelled
	order.Reason = "已撤"
	if err := e.store.Commit(ctx, Batch{
		Orders: []Order{order},
		Trails: []Trail{{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: order.Reason}},
		Events: []Event{{Subject: events.SubjectTradeOrder, Payload: orderPayload(order)}},
	}); err != nil {
		return Order{}, err
	}
	return order, nil
}

func (e *Engine) Get(ctx context.Context, clientID string) (Order, error) {
	order, ok, err := e.store.LoadOrder(ctx, strings.TrimSpace(clientID))
	if err != nil {
		return Order{}, err
	}
	if !ok {
		return Order{}, ErrNotFound
	}
	return order, nil
}

func (e *Engine) Positions(ctx context.Context, account string) ([]Position, error) {
	book, err := bookOf(account)
	if err != nil {
		return nil, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	return e.store.ListPositions(ctx, book)
}

func (e *Engine) Account(ctx context.Context, account string) (AccountView, error) {
	book, err := bookOf(account)
	if err != nil {
		return AccountView{}, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	return e.view(ctx, book)
}

func (e *Engine) Snapshot(ctx context.Context, account string) (AccountView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	book, err := bookOf(account)
	if err != nil {
		return AccountView{}, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	view, err := e.view(ctx, book)
	if err != nil {
		return AccountView{}, err
	}
	if err := e.store.SaveSnapshot(ctx, book, view); err != nil {
		return AccountView{}, err
	}
	if err := e.store.Commit(ctx, Batch{Events: []Event{{Subject: events.SubjectTradeAccount, Payload: view}}}); err != nil {
		return AccountView{}, err
	}
	return view, nil
}

func (e *Engine) view(ctx context.Context, book string) (AccountView, error) {
	cash, err := e.cash(ctx, book)
	if err != nil {
		return AccountView{}, err
	}
	pos, err := e.store.ListPositions(ctx, book)
	if err != nil {
		return AccountView{}, err
	}
	view := AccountView{
		Book: book, Cash: cash.Cash, RealizedPnL: cash.RealizedPnL,
		PaperDays: cash.PaperDays, OpenHalted: cash.OpenHalted, HaltReason: cash.HaltReason,
	}
	for _, p := range pos {
		if p.Quantity <= 0 {
			continue
		}
		px := p.LastPrice
		if px <= 0 {
			px = p.AvgCost
		}
		view.Equity += px * float64(p.Quantity)
		view.UnrealizedPnL += (px - p.AvgCost) * float64(p.Quantity)
		view.PositionCount++
	}
	view.Equity += cash.Cash
	return view, nil
}

// RollDay 把当日买入转为可卖，并把模拟盘交易日加一。
func (e *Engine) RollDay(ctx context.Context, account string) (AccountView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	book, err := bookOf(account)
	if err != nil {
		return AccountView{}, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	cash, err := e.cash(ctx, book)
	if err != nil {
		return AccountView{}, err
	}
	pos, err := e.store.ListPositions(ctx, book)
	if err != nil {
		return AccountView{}, err
	}
	for i := range pos {
		pos[i].Available = pos[i].Quantity
	}
	if book == BookPaper {
		cash.PaperDays++
	}
	if err := e.store.Commit(ctx, Batch{Cash: &cash, Positions: pos}); err != nil {
		return AccountView{}, err
	}
	return e.view(ctx, book)
}

// Reconcile 用外部持仓校正。数量不一致则停止该账本开仓。
func (e *Engine) Reconcile(ctx context.Context, account string, remote []RemotePosition) (bool, []string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	book, err := bookOf(account)
	if err != nil {
		return false, nil, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	local, err := e.store.ListPositions(ctx, book)
	if err != nil {
		return false, nil, err
	}
	want := map[string]RemotePosition{}
	for _, r := range remote {
		want[r.Symbol] = r
	}
	var diffs []string
	seen := map[string]bool{}
	for _, p := range local {
		if p.Quantity == 0 {
			continue
		}
		seen[p.Symbol] = true
		r, ok := want[p.Symbol]
		if !ok || r.Quantity != p.Quantity {
			diffs = append(diffs, fmt.Sprintf("%s 本地 %d 外部 %d", p.Symbol, p.Quantity, r.Quantity))
		}
	}
	for _, r := range remote {
		if seen[r.Symbol] || r.Quantity == 0 {
			continue
		}
		diffs = append(diffs, fmt.Sprintf("%s 本地 0 外部 %d", r.Symbol, r.Quantity))
	}
	cash, err := e.cash(ctx, book)
	if err != nil {
		return false, nil, err
	}
	if len(diffs) > 0 {
		cash.OpenHalted = true
		cash.HaltReason = "持仓对账不平"
	}
	if err := e.store.Commit(ctx, Batch{
		Cash:   &cash,
		Events: []Event{{Subject: events.SubjectTradeAccount, Payload: map[string]any{"book": book, "halted": cash.OpenHalted, "diffs": diffs}}},
	}); err != nil {
		return false, nil, err
	}
	return len(diffs) == 0, diffs, nil
}

func (e *Engine) cash(ctx context.Context, book string) (Cash, error) {
	cash, ok, err := e.store.LoadCash(ctx, book)
	if err != nil {
		return Cash{}, err
	}
	if !ok {
		return Cash{Book: book, Cash: e.set.InitialCash}, nil
	}
	cash.Book = book
	return cash, nil
}

func (e *Engine) clock() time.Time {
	if e != nil && e.now != nil {
		return e.now()
	}
	return time.Now()
}

func confirmExpired(order Order, now time.Time) bool {
	if order.CreatedAt.IsZero() {
		return false
	}
	return !now.Before(order.CreatedAt.Add(ConfirmWindow))
}

func confirmPayload(o Order) map[string]any {
	return map[string]any{
		"kind":            "confirm",
		"account":         "LIVE",
		"client_order_id": o.ClientOrderID,
		"symbol":          o.Symbol,
		"side":            o.Side,
		"price":           o.Price,
		"volume":          o.Volume,
		"title":           "实盘委托待确认",
		"body":            fmt.Sprintf("%s %s %d股 @ %.2f", o.Symbol, o.Side, o.Volume, o.Price),
		"need_confirm":    true,
	}
}

// Confirm 把待确认的实盘委托放进撮合。超时则作废。
func (e *Engine) Confirm(ctx context.Context, clientID string) (Order, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	order, ok, err := e.store.LoadOrder(ctx, strings.TrimSpace(clientID))
	if err != nil {
		return Order{}, err
	}
	if !ok {
		return Order{}, ErrNotFound
	}
	if order.Status != StatusPending {
		return Order{}, fmt.Errorf("%w: 当前状态 %s 不能确认", ErrInvalid, order.Status)
	}
	if confirmExpired(order, e.clock()) {
		if _, err := e.void(ctx, order, "确认超时作废"); err != nil {
			return Order{}, err
		}
		return Order{}, fmt.Errorf("%w: 确认超时作废", ErrInvalid)
	}
	order.Status = StatusSubmitted
	order.Reason = "已确认"
	if err := e.store.Commit(ctx, Batch{
		Orders: []Order{order},
		Trails: []Trail{{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: order.Reason}},
		Events: []Event{{Subject: events.SubjectTradeOrder, Payload: orderPayload(order)}},
	}); err != nil {
		return Order{}, err
	}
	return order, nil
}

// ExpireConfirms 作废超过确认窗的实盘委托。
func (e *Engine) ExpireConfirms(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	rows, err := e.store.ListByStatus(ctx, StatusPending)
	if err != nil {
		return err
	}
	now := e.clock()
	for _, order := range rows {
		if !confirmExpired(order, now) {
			continue
		}
		if _, err := e.void(ctx, order, "确认超时作废"); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) void(ctx context.Context, order Order, reason string) (Order, error) {
	order.Status = StatusCancelled
	order.Reason = reason
	if err := e.store.Commit(ctx, Batch{
		Orders: []Order{order},
		Trails: []Trail{{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: reason}},
		Events: []Event{{Subject: events.SubjectTradeOrder, Payload: orderPayload(order)}},
	}); err != nil {
		return Order{}, err
	}
	return order, nil
}

func (e *Engine) Orders(ctx context.Context, account string) ([]Order, error) {
	book, err := bookOf(account)
	if err != nil {
		return nil, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	return e.store.ListOrders(ctx, book, 100)
}

func (e *Engine) Fills(ctx context.Context, account string) ([]Fill, error) {
	book, err := bookOf(account)
	if err != nil {
		return nil, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	return e.store.ListFills(ctx, book, 100)
}

func orderPayload(o Order) map[string]any {
	return map[string]any{
		"client_order_id":  o.ClientOrderID,
		"book":             o.Book,
		"symbol":           o.Symbol,
		"side":             o.Side,
		"status":           o.Status,
		"price":            o.Price,
		"volume":           o.Volume,
		"filled":           o.Filled,
		"reason":           o.Reason,
		"signal_id":        o.SignalID,
		"strategy_version": o.StrategyVersion,
	}
}
