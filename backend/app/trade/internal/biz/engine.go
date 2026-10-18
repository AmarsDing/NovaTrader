package biz

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"server/pkg/ashare"
	"server/pkg/brokerif"
	"server/pkg/brokerif/sim"
	"server/pkg/events"
	"server/pkg/symbol"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

// Settings 是模拟撮合和实盘门槛。
type Settings struct {
	InitialCash       float64
	PaperDaysRequired int
	Fee               ashare.Fee
	Match             sim.MatchRule
	LiveEnabled       bool
	BrokerConnected   bool // Only a verified broker adapter may set this; no config/UI bypass.
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
	if s.PaperDaysRequired < 20 {
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
	return e.placeDurable(ctx, req)
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
	if req.SignalID != "" {
		if validator, ok := e.store.(SignalOrderValidator); ok {
			if err := validator.ValidateSignalOrder(ctx, req, book, e.clock()); err != nil {
				return Order{}, err
			}
		}
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
		order.Channel = "eastmoney-file"
		if !e.set.BrokerConnected {
			return reject("实盘柜台通道尚未接入或未完成对账")
		}
		if !e.set.LiveEnabled {
			return reject("实盘通道未打开")
		}
		evidence, err := e.PaperAdmission(ctx, "SIM", 40)
		if err != nil || !evidence.RunEvidenceReady || evidence.RequiredDays < 20 {
			return reject("连续真实交易日运行证据未通过；旧paper_days不作为准入依据")
		}
	}
	if err := e.prepareDay(ctx, book, true, false); err != nil {
		return Order{}, err
	}
	cash, err = e.cash(ctx, book)
	if err != nil {
		return Order{}, err
	}
	if req.Side == string(brokerif.Buy) && cash.OpenHalted {
		return reject("对账不平，已停止开仓")
	}
	frozenCash, frozenSells, err := e.reserved(ctx, book, order.ClientOrderID, nil)
	if err != nil {
		return Order{}, err
	}
	if req.Side == string(brokerif.Buy) {
		n, err := ashare.RoundBuy(req.Symbol, req.Volume)
		if err != nil || n != req.Volume {
			return reject("买入数量不是合法手数")
		}
	}

	out, err := e.risk.Check(ctx, CheckIn{
		ExpectedConfigRevision: req.ExpectedConfigRevision,
		ClientOrderID:          req.ClientOrderID, Account: accountOf(book), Symbol: req.Symbol,
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
	if req.Side == "buy" && e.frozenBuy(order) > cash.Cash-frozenCash+0.001 {
		return reject("可用资金不足（含在途委托冻结和费用）")
	}

	pos, _, err := e.store.LoadPosition(ctx, book, req.Symbol)
	if err != nil {
		return Order{}, err
	}
	pos.Book = book
	pos.Symbol = req.Symbol
	if req.Side == string(brokerif.Sell) {
		ok, err := ashare.CanSell(req.Symbol, pos.Available-frozenSells[req.Symbol], order.Volume)
		if err != nil || !ok {
			return reject("可卖数量不足（含 T+1）")
		}
	}

	// 盯盘卖出是保护性退出，不等 30 秒确认窗。开仓和手动单仍要确认。
	if req.FinalCheck != nil {
		if err := req.FinalCheck(ctx); err != nil {
			return Order{}, err
		}
	}
	if book == BookLive {
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
		if err := e.fill(ctx, &batch, &order, pos, *req.Quote); err != nil {
			return Order{}, err
		}
		batch.Orders = []Order{order}
	}
	batch.Events = append(batch.Events, Event{Subject: events.SubjectTradeOrder, Payload: orderPayload(order)})
	if err := e.store.Commit(ctx, batch); err != nil {
		return Order{}, err
	}
	return order, nil
}

func (e *Engine) fill(ctx context.Context, batch *Batch, order *Order, pos Position, q Quote) error {
	return e.fillWithPool(ctx, batch, order, pos, q, nil)
}

func (e *Engine) fillWithPool(ctx context.Context, batch *Batch, order *Order, pos Position, q Quote, pool *int) error {
	if order.Book == BookLive {
		return fmt.Errorf("%w: 实盘只能消费柜台回报，不能使用模拟撮合", ErrInvalid)
	}
	if !q.Time.IsZero() && q.Time.Before(order.CreatedAt) {
		return nil
	}
	if order.Status != StatusSubmitted && order.Status != StatusPartial {
		return nil
	}
	remain := order.Volume - order.Filled
	matchRule := e.set.Match
	if pool != nil {
		if *pool <= 0 {
			order.Reason = "本分钟可撮合量已用尽"
			return nil
		}
		if remain > *pool {
			remain = *pool
		}
		// The shared pool already applied the participation ratio once.
		matchRule.VolumeCap = 0
	}
	side := brokerif.Side(order.Side)
	got := sim.Match(order.Symbol, side, remain, pos.Available, order.Price, sim.Quote{
		Open: q.Open, High: q.High, Low: q.Low, Volume: q.Volume,
		LimitUp: q.LimitUp, LimitDown: q.LimitDown,
	}, matchRule)
	if got.Qty <= 0 {
		order.Reason = got.Reason
		return nil
	}
	cash := *batch.Cash
	qty := got.Qty
	if side == brokerif.Buy {
		frozen, _, err := e.reserved(ctx, order.Book, order.ClientOrderID, batch.Orders)
		if err != nil {
			return err
		}
		qty = afford(e.set.Fee, cash.Cash-frozen, order.Symbol, qty, got.Price)
		if qty <= 0 {
			order.Reason = "资金不足"
			return nil
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
	if pool != nil {
		*pool -= qty
	}
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
	return nil
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

func (e *Engine) applyBar(ctx context.Context, account, rawSymbol string, q Quote) ([]Order, error) {
	book, err := bookOf(account)
	if err != nil {
		return nil, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	if book == BookLive {
		return nil, fmt.Errorf("%w: 实盘不能用K线撮合", ErrInvalid)
	}
	sym := strings.TrimSpace(rawSymbol)
	if err := e.prepareDay(ctx, book, true, false); err != nil {
		return nil, err
	}
	orders, err := e.store.ListOpen(ctx, book, sym)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		// Holdings still need mark-to-market updates after their opening order
		// has filled. A fresh account timestamp cannot justify a frozen old mark.
		if q.Close > 0 {
			pos, ok, err := e.store.LoadPosition(ctx, book, sym)
			if err != nil {
				return nil, err
			}
			if ok && pos.Quantity > 0 {
				pos.LastPrice = q.Close
				if err := e.store.Commit(ctx, Batch{Positions: []Position{pos}}); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	}
	cash, err := e.cash(ctx, book)
	if err != nil {
		return nil, err
	}
	batch := Batch{Cash: &cash}
	var out []Order
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].CreatedAt.Equal(orders[j].CreatedAt) {
			return orders[i].ClientOrderID < orders[j].ClientOrderID
		}
		return orders[i].CreatedAt.Before(orders[j].CreatedAt)
	})
	participation := e.set.Match.VolumeCap
	if participation <= 0 || participation > 1 || math.IsNaN(participation) || math.IsInf(participation, 0) {
		participation = 1
	}
	pool := int(math.Floor(float64(q.Volume) * participation))
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
		if err := e.fillWithPool(ctx, &batch, &orders[i], pos, q, &pool); err != nil {
			return nil, err
		}
		batch.Orders = append(batch.Orders, orders[i])
		out = append(out, orders[i])
	}
	if q.Close > 0 {
		if pos, ok, err := e.store.LoadPosition(ctx, book, sym); err != nil {
			return nil, err
		} else if ok {
			pos = overlay(batch.Positions, pos)
			pos.LastPrice = q.Close
			batch.Positions = upsertPos(batch.Positions, pos)
		}
	}
	for _, order := range out {
		batch.Events = append(batch.Events, Event{Subject: events.SubjectTradeOrder, Payload: orderPayload(order)})
	}
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

func (e *Engine) cancelOrder(ctx context.Context, clientID string) (Order, error) {
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

func (e *Engine) snapshotAccount(ctx context.Context, account string) (AccountView, error) {
	book, err := bookOf(account)
	if err != nil {
		return AccountView{}, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	return e.snapshot(ctx, book)
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
func (e *Engine) rollDay(ctx context.Context, account string) (AccountView, error) {
	book, err := bookOf(account)
	if err != nil {
		return AccountView{}, fmt.Errorf("%w: 账户只能是 SIM 或 LIVE", ErrInvalid)
	}
	if err := e.prepareDay(ctx, book, true, true); err != nil {
		return AccountView{}, err
	}
	return e.view(ctx, book)
}

// Reconcile 用外部持仓校正。数量不一致则停止该账本开仓。
func (e *Engine) reconcile(ctx context.Context, account string, remote []RemotePosition) (bool, []string, error) {
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
		if !ok || r.Quantity != p.Quantity || r.Available != p.Available {
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
		Events: []Event{{Subject: events.SubjectTradeReconciliation, Payload: map[string]any{"version": 1, "account_type": accountOf(book), "account_id": "default", "book": book, "halted": cash.OpenHalted, "diffs": diffs, "as_of": e.clock()}}},
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
		if book == BookLive {
			return Cash{Book: book}, nil
		}
		return Cash{Book: book, Cash: e.set.InitialCash, DayStartDate: e.clock().In(tradecal.Shanghai()).Format("2006-01-02"), DayStartEquity: e.set.InitialCash}, nil
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
func (e *Engine) confirm(ctx context.Context, clientID string) (Order, error) {
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
	if order.Book == BookLive && !e.set.BrokerConnected {
		return Order{}, fmt.Errorf("%w: 柜台通道未连接", ErrInvalid)
	}
	check, err := e.risk.Check(ctx, CheckIn{ClientOrderID: order.ClientOrderID, Account: accountOf(order.Book), Symbol: order.Symbol, Side: order.Side, Price: order.Price, Volume: order.Volume, Source: order.Source, Operator: order.Operator})
	if err != nil || !check.Approved {
		return Order{}, fmt.Errorf("%w: 确认时最新风控未通过", ErrInvalid)
	}
	if check.Volume > 0 && check.Volume < order.Volume {
		order.Volume = check.Volume
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
func (e *Engine) expireConfirms(ctx context.Context) error {
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
