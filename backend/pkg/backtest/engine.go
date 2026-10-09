package backtest

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
	"sort"
	"strings"
	"time"

	"server/pkg/ashare"
	"server/pkg/brokerif"
	"server/pkg/brokerif/sim"
	"server/pkg/rules"
	"server/pkg/tradecal"
)

// Pricer 是策略可选实现的接口：下单决策时按计划价给出买入限价、止损价、止盈价，与实盘信号的价位一致。
// 止损止盈写进持仓，供 ExitPlan 使用。返回错误时这次不下单。
type Pricer interface {
	BuyLevels(s rules.Snapshot, ref float64) (limit, stop, take float64, err error)
}

type symData struct {
	bars     []DayBar
	cur      int // bars[:cur] 早于今天
	names    []NameSpan
	listDate time.Time
}

// dayView 是一只股票当天不变的部分：复权后的已收盘日线、ST、涨跌停价。
type dayView struct {
	bars     []rules.Bar
	name     string
	st       bool
	alive    bool
	up, down float64
	today    *DayBar
	agg      *rules.Bar
}

type order struct {
	symbol   string
	side     string
	qty      int
	limit    float64
	stop     float64
	take     float64
	reason   string
	snapshot rules.Snapshot
}

type holding struct {
	openIdx int
	round   *Round
	stop    float64
	take    float64
}

type engine struct {
	cfg   Config
	strat rules.Strategy
	src   Source
	rule  sim.MatchRule

	days     []time.Time
	data     map[string]*symData
	symbols  []string
	bench    []DayBar
	phases   []PhasePoint
	firstDay time.Time
	prevDay  time.Time

	acct    *sim.Account
	hold    map[string]*holding
	last    map[string]float64
	view    map[string]*dayView
	pending map[string]*order

	dayIdx   int
	today    time.Time
	orders   []OrderLog
	trades   []Trade
	rounds   []Round
	oid      int
	roundSeq int
	rejects  map[string]int
	equity   []EquityPoint
	warn     []string
	warned   map[string]bool
	data0    hash.Hash
	cutoff   time.Time
}

// Run 执行一次回测。progress 可为 nil。
func Run(ctx context.Context, cfg Config, src Source, progress Progress) (*Result, error) {
	start, end, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	spec, _ := rules.Find(cfg.Strategy)
	strat, err := spec.New(cfg.Params)
	if err != nil {
		return nil, err
	}
	e := &engine{
		cfg:     cfg,
		strat:   strat,
		src:     src,
		rule:    sim.MatchRule{SlippageTicks: cfg.SlippageTicks, VolumeCap: cfg.VolumeCap, LimitFill: cfg.LimitFill},
		acct:    sim.NewAccount(cfg.InitialCash, cfg.Fee.Fee()),
		hold:    map[string]*holding{},
		last:    map[string]float64{},
		pending: map[string]*order{},
		rejects: map[string]int{},
		warned:  map[string]bool{},
		data0:   sha256.New(),
	}
	if err := e.load(ctx, start, end); err != nil {
		return nil, err
	}
	for i, d := range e.days {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e.dayIdx, e.today = i, d
		if i > 0 {
			e.prevDay = e.days[i-1]
		}
		if err := e.prepareDay(d); err != nil {
			return nil, err
		}
		if cfg.Freq == Freq1m {
			if err := e.runMinutes(ctx, d); err != nil {
				return nil, err
			}
			e.closeDay(d)
		} else {
			e.fillDaily(ctx, d)
			e.closeDay(d)
			e.signalDaily(ctx, d)
		}
		if progress != nil {
			progress(i+1, len(e.days))
		}
	}
	for range e.pending {
		e.rejects["区间结束"]++
	}
	return e.result(), nil
}

func (e *engine) load(ctx context.Context, start, end time.Time) error {
	days, err := e.src.TradingDays(ctx, start, end)
	if err != nil {
		return err
	}
	if len(days) == 0 {
		return fmt.Errorf("backtest: %s 至 %s 没有交易日", dayKey(start), dayKey(end))
	}
	e.days = days
	from := start.AddDate(0, 0, -(e.cfg.HistoryBars*3/2 + 10))
	daily, err := e.src.DailyBars(ctx, from, end)
	if err != nil {
		return err
	}
	names, err := e.src.Names(ctx)
	if err != nil {
		return err
	}
	lists, err := e.src.ListDates(ctx)
	if err != nil {
		return err
	}
	if e.phases, err = e.src.Phases(ctx, from, end.AddDate(0, 0, 1)); err != nil {
		return err
	}
	e.data = map[string]*symData{}
	keys := make([]string, 0, len(daily))
	for sym := range daily {
		keys = append(keys, sym)
	}
	sort.Strings(keys)
	for _, sym := range keys {
		bars := daily[sym]
		e.hashBars(sym, bars)
		if sym == e.cfg.Benchmark {
			e.bench = bars
		}
		if !ashare.IsAStock(sym) || len(bars) == 0 {
			continue
		}
		e.data[sym] = &symData{bars: bars, names: names[sym], listDate: lists[sym]}
		e.symbols = append(e.symbols, sym)
		if e.firstDay.IsZero() || bars[0].Day.Before(e.firstDay) {
			e.firstDay = bars[0].Day
		}
		if last := bars[len(bars)-1].Day; last.After(e.cutoff) {
			e.cutoff = last
		}
	}
	if len(e.symbols) == 0 {
		return fmt.Errorf("backtest: %s 至 %s 没有 A 股日线", dayKey(from), dayKey(end))
	}
	if len(names) == 0 {
		e.warnOnce("缺少名称史（stock_name_history），ST 判定按非 ST 处理，结果可能偏乐观")
	}
	if len(e.phases) == 0 {
		e.warnOnce("缺少情绪阶段历史（market_sentiment），阶段一律按 WARM")
	}
	if len(e.bench) == 0 {
		e.warnOnce("缺少基准 " + e.cfg.Benchmark + " 日线，不做基准对比")
	}
	e.warnOnce("回测不重放消息面：NegativeNews 恒为 false，模型分不参与")
	for _, s := range e.symbols {
		h := 0
		sd := e.data[s]
		for h < len(sd.bars) && sd.bars[h].Day.Before(days[0]) {
			h++
		}
		if h > 0 && sd.bars[h-1].Day.After(e.prevDay) {
			e.prevDay = sd.bars[h-1].Day
		}
	}
	return nil
}

func (e *engine) warnOnce(msg string) {
	if !e.warned[msg] {
		e.warned[msg] = true
		e.warn = append(e.warn, msg)
	}
}

func (e *engine) hashBars(sym string, bars []DayBar) {
	e.data0.Write([]byte(sym))
	var buf [8]byte
	for _, b := range bars {
		binary.LittleEndian.PutUint64(buf[:], uint64(b.Day.Unix()))
		e.data0.Write(buf[:])
		for _, v := range []float64{b.Open, b.High, b.Low, b.Close, b.Amount, b.Adj, b.PreClose} {
			binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v))
			e.data0.Write(buf[:])
		}
		binary.LittleEndian.PutUint64(buf[:], uint64(b.Volume))
		e.data0.Write(buf[:])
	}
}

func (e *engine) hashMinutes(day time.Time, mins map[string][]MinBar, syms []string) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(day.Unix()))
	e.data0.Write(buf[:])
	for _, sym := range syms {
		e.data0.Write([]byte(sym))
		for _, b := range mins[sym] {
			binary.LittleEndian.PutUint64(buf[:], uint64(b.Time.Unix()))
			e.data0.Write(buf[:])
			for _, v := range []float64{b.Open, b.High, b.Low, b.Close, b.Amount} {
				binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v))
				e.data0.Write(buf[:])
			}
			binary.LittleEndian.PutUint64(buf[:], uint64(b.Volume))
			e.data0.Write(buf[:])
		}
	}
}

// prepareDay 推进游标，为前一交易日有行情或仍有持仓的股票建当日视图，并处理除权入账。
func (e *engine) prepareDay(d time.Time) error {
	e.view = map[string]*dayView{}
	for _, sym := range e.symbols {
		sd := e.data[sym]
		for sd.cur < len(sd.bars) && sd.bars[sd.cur].Day.Before(d) {
			sd.cur++
		}
		_, held := e.hold[sym]
		if sd.cur == 0 {
			continue
		}
		prev := sd.bars[sd.cur-1]
		alive := prev.Day.Equal(e.prevDay)
		if !alive && !held {
			continue
		}
		var today *DayBar
		if sd.cur < len(sd.bars) && sd.bars[sd.cur].Day.Equal(d) {
			today = &sd.bars[sd.cur]
		}
		ref := adj(prev.Adj)
		if today != nil {
			ref = adj(today.Adj)
		}
		from := sd.cur - e.cfg.HistoryBars
		if from < 0 {
			from = 0
		}
		bars := make([]rules.Bar, 0, sd.cur-from)
		for _, b := range sd.bars[from:sd.cur] {
			k := adj(b.Adj) / ref
			bars = append(bars, rules.Bar{Time: b.Day, Open: b.Open * k, High: b.High * k, Low: b.Low * k, Close: b.Close * k, Volume: b.Volume, Amount: b.Amount})
		}
		name, st := nameOn(sd.names, d)
		if name == "" {
			name = sym
		}
		base := prev.Close * adj(prev.Adj) / ref
		if today != nil && today.PreClose > 0 {
			base = today.PreClose
		}
		ratio, err := ashare.RatioOn(sym, st, d)
		if err != nil {
			return err
		}
		up, down := ashare.Limit(base, ratio)
		if e.newListing(sd, d) {
			up, down = 0, 0
		}
		e.view[sym] = &dayView{bars: bars[:len(bars):len(bars)], name: name, st: st, alive: alive, up: up, down: down, today: today}
		if _, ok := e.last[sym]; !ok || !held {
			e.last[sym] = prev.Close
		}
		if h := e.hold[sym]; h != nil && today != nil {
			e.exRights(sym, h, prev, *today, d)
		}
	}
	return nil
}

func adj(f float64) float64 {
	if f <= 0 {
		return 1
	}
	return f
}

// nameOn 返回 d 当天的名称和是否 ST。
func nameOn(spans []NameSpan, d time.Time) (string, bool) {
	for i := len(spans) - 1; i >= 0; i-- {
		s := spans[i]
		if !s.Start.After(d) && (s.End.IsZero() || !s.End.Before(d)) {
			return s.Name, strings.Contains(strings.ToUpper(s.Name), "ST")
		}
	}
	return "", false
}

// newListing 判断是否处于上市后不设涨跌幅的前 ashare.NoLimitDays 个交易日。
// 没有上市日期时，首根日线晚于整份数据的首日才认为是新股；首日就有数据的无从判断，按老股处理。
func (e *engine) newListing(sd *symData, d time.Time) bool {
	first := sd.bars[0].Day
	if !sd.listDate.IsZero() {
		first = Day(sd.listDate)
		if first.Before(e.firstDay) {
			return false
		}
	} else if !first.After(e.firstDay) {
		return false
	}
	n := 0
	for i := sd.cur - 1; i >= 0 && n < ashare.NoLimitDays && !sd.bars[i].Day.Before(first); i-- {
		n++
	}
	return n < ashare.NoLimitDays
}

// exRights 持仓跨除权日：按 q × 前收 × (1 − f0/f1) 记入现金，计入该回合盈亏。
func (e *engine) exRights(sym string, h *holding, prev, today DayBar, d time.Time) {
	f0, f1 := adj(prev.Adj), adj(today.Adj)
	if math.Abs(f1-f0) <= 1e-9*f0 {
		return
	}
	pos := e.acct.Book.Position(sym)
	if pos.Quantity <= 0 {
		return
	}
	credit := math.Round(float64(pos.Quantity)*prev.Close*(1-f0/f1)*100) / 100
	e.acct.Credit(credit)
	h.round.Income += credit
	e.trades = append(e.trades, Trade{
		Seq: len(e.trades) + 1, Symbol: sym, Side: "dividend", Time: d.Add(9*time.Hour + 15*time.Minute),
		Qty: pos.Quantity, Amount: credit, Reason: "除权等值入账", RoundID: h.round.ID,
	})
}

func (e *engine) stageAt(t time.Time) rules.Stage {
	i := sort.Search(len(e.phases), func(i int) bool { return e.phases[i].AsOf.After(t) })
	if i == 0 {
		return rules.StageWarm
	}
	return rules.Stage(e.phases[i-1].Phase)
}

func (e *engine) snapshot(sym string, asOf time.Time, today *rules.Bar) rules.Snapshot {
	v := e.view[sym]
	var t *rules.Bar
	if today != nil {
		c := *today
		t = &c
	}
	return rules.Snapshot{
		Symbol: sym, Name: v.name, ST: v.st, Suspended: t == nil && !v.alive,
		ListDate: e.data[sym].listDate, AsOf: asOf,
		Bars: v.bars, Today: t, Stage: e.stageAt(asOf),
	}
}

func (e *engine) position(sym string) rules.Position {
	p := e.acct.Book.Position(sym)
	rp := rules.Position{Symbol: sym, Quantity: p.Quantity, Available: p.Available, AvgCost: p.AvgCost}
	if h := e.hold[sym]; h != nil {
		rp.StopLoss, rp.TakeProfit, rp.HoldDays = h.stop, h.take, e.dayIdx-h.openIdx
	}
	return rp
}

func (e *engine) slotsUsed() int {
	n := len(e.hold)
	for _, o := range e.pending {
		if o.side == rules.SideBuy {
			n++
		}
	}
	return n
}

func (e *engine) equityNow() float64 {
	v := e.acct.Cash
	for sym := range e.hold {
		v += float64(e.acct.Book.Position(sym).Quantity) * e.last[sym]
	}
	return v
}

type cand struct {
	sym   string
	score float64
}

// rank 对没持仓的股票做 Filter + Score，按分数降序、代码升序，取前 limit 个。
func (e *engine) rank(ctx context.Context, asOf time.Time, todayOf func(string) *rules.Bar, limit int) []cand {
	var out []cand
	for _, sym := range e.symbols {
		v := e.view[sym]
		if v == nil || (todayOf == nil && !v.alive) {
			continue
		}
		if _, held := e.hold[sym]; held {
			continue
		}
		var today *rules.Bar
		if todayOf != nil {
			if today = todayOf(sym); today == nil {
				continue
			}
		}
		s := e.snapshot(sym, asOf, today)
		if !e.strat.Filter(ctx, s) {
			continue
		}
		out = append(out, cand{sym: sym, score: e.strat.Score(ctx, s)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].sym < out[j].sym
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (e *engine) heldSymbols() []string {
	out := make([]string, 0, len(e.hold))
	for sym := range e.hold {
		out = append(out, sym)
	}
	sort.Strings(out)
	return out
}

func at(d time.Time, hour, min int) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), hour, min, 0, 0, tradecal.Shanghai())
}

func (e *engine) runMinutes(ctx context.Context, d time.Time) error {
	cands := e.rank(ctx, at(d, 9, 25), nil, e.cfg.MaxCandidates)
	watch := e.heldSymbols()
	for _, c := range cands {
		watch = append(watch, c.sym)
	}
	if len(watch) == 0 {
		return nil
	}
	sorted := append([]string(nil), watch...)
	sort.Strings(sorted)
	mins, err := e.src.MinuteBars(ctx, d, sorted)
	if err != nil {
		return err
	}
	e.hashMinutes(d, mins, sorted)
	var timeline []time.Time
	seen := map[int64]bool{}
	for _, sym := range sorted {
		for _, b := range mins[sym] {
			if k := b.Time.Unix(); !seen[k] {
				seen[k] = true
				timeline = append(timeline, b.Time)
			}
		}
	}
	sort.Slice(timeline, func(i, j int) bool { return timeline[i].Before(timeline[j]) })
	cur := map[string]int{}
	for _, t := range timeline {
		present := map[string]MinBar{}
		for _, sym := range sorted {
			bars, i := mins[sym], cur[sym]
			if i < len(bars) && bars[i].Time.Equal(t) {
				present[sym] = bars[i]
				cur[sym] = i + 1
			}
		}
		for _, sym := range sorted {
			if o := e.pending[sym]; o != nil {
				if b, ok := present[sym]; ok {
					e.execute(ctx, o, sim.Quote{Open: b.Open, High: b.High, Low: b.Low, Volume: b.Volume}, b.Time)
					delete(e.pending, sym)
				}
			}
		}
		for sym, b := range present {
			v := e.view[sym]
			if v == nil {
				continue
			}
			if v.agg == nil {
				v.agg = &rules.Bar{Time: d, Open: b.Open, High: b.High, Low: b.Low}
			}
			v.agg.High = math.Max(v.agg.High, b.High)
			v.agg.Low = math.Min(v.agg.Low, b.Low)
			v.agg.Close = b.Close
			v.agg.Volume += b.Volume
			v.agg.Amount += b.Amount
			e.last[sym] = b.Close
		}
		asOf := t.Add(time.Minute)
		for _, sym := range watch {
			v := e.view[sym]
			if v == nil || v.agg == nil || e.pending[sym] != nil {
				continue
			}
			e.decide(ctx, sym, e.snapshot(sym, asOf, v.agg))
		}
	}
	for sym := range e.pending {
		e.rejects["收盘作废"]++
		delete(e.pending, sym)
	}
	return nil
}

// decide 为一只股票生成至多一张委托：持仓且可卖时问 ExitPlan，没持仓且有空位时问 EntryPlan。
func (e *engine) decide(ctx context.Context, sym string, s rules.Snapshot) {
	if _, held := e.hold[sym]; held {
		pos := e.position(sym)
		if pos.Available <= 0 {
			return
		}
		plan, ok := e.strat.ExitPlan(ctx, s, pos)
		if !ok || plan.Side != rules.SideSell {
			return
		}
		qty := pos.Available
		if plan.Shares > 0 && plan.Shares < qty {
			qty = plan.Shares
		}
		if ok, _ := ashare.CanSell(sym, pos.Available, qty); !ok {
			qty = pos.Available
		}
		e.place(&order{symbol: sym, side: rules.SideSell, qty: qty, limit: plan.Price, reason: exitReason(plan), snapshot: s})
		return
	}
	if e.slotsUsed() >= e.cfg.MaxPositions {
		return
	}
	plan, ok := e.strat.EntryPlan(ctx, s)
	if !ok || plan.Side != rules.SideBuy {
		return
	}
	ref := plan.Price
	if ref <= 0 {
		ref = s.Last()
	}
	if ref <= 0 {
		return
	}
	o := &order{symbol: sym, side: rules.SideBuy, limit: plan.Price, reason: plan.Reason, snapshot: s}
	if p, ok := e.strat.(Pricer); ok {
		limit, stop, take, err := p.BuyLevels(s, ref)
		if err != nil {
			e.rejects["价位无效"]++
			return
		}
		o.limit, o.stop, o.take = limit, stop, take
	}
	qty := plan.Shares
	if qty <= 0 {
		qty = int(e.equityNow() * e.cfg.PositionPct / ref)
	}
	qty, _ = ashare.RoundBuy(sym, qty)
	if qty <= 0 {
		e.rejects["仓位不足一手"]++
		return
	}
	o.qty = qty
	e.place(o)
}

func (e *engine) place(o *order) {
	e.pending[o.symbol] = o
	e.orders = append(e.orders, OrderLog{Time: o.snapshot.AsOf, Symbol: o.symbol, Side: o.side, Qty: o.qty, Limit: o.limit, Reason: o.reason})
}

func exitReason(p rules.Plan) string {
	if p.Reason != "" {
		return p.Reason
	}
	return p.ExitKind
}

func (e *engine) execute(ctx context.Context, o *order, q sim.Quote, t time.Time) {
	v := e.view[o.symbol]
	if v == nil {
		e.rejects["停牌或无成交"]++
		return
	}
	q.LimitUp, q.LimitDown = v.up, v.down
	side := brokerif.Buy
	if o.side == rules.SideSell {
		side = brokerif.Sell
	}
	pos := e.acct.Book.Position(o.symbol)
	f := sim.Match(o.symbol, side, o.qty, pos.Available, o.limit, q, e.rule)
	if f.Qty == 0 {
		e.rejects[f.Reason]++
		return
	}
	e.oid++
	id := fmt.Sprintf("bt-%d", e.oid)
	if side == brokerif.Buy {
		n := e.acct.Affordable(o.symbol, f.Qty, f.Price)
		if n == 0 {
			e.rejects["资金不足"]++
			return
		}
		ex, err := e.acct.Buy(ctx, id, o.symbol, n, f.Price)
		if err != nil {
			e.rejects["资金不足"]++
			return
		}
		h := e.hold[o.symbol]
		if h == nil {
			e.roundSeq++
			h = &holding{openIdx: e.dayIdx, stop: o.stop, take: o.take, round: &Round{ID: e.roundSeq, Symbol: o.symbol, OpenTime: t, openIdx: e.dayIdx}}
			e.hold[o.symbol] = h
		}
		h.round.BuyAmount += ex.Amount
		h.round.Fees += ex.Fee
		e.trades = append(e.trades, Trade{Seq: len(e.trades) + 1, Symbol: o.symbol, Side: rules.SideBuy, Time: t, Price: ex.Price, Qty: ex.Qty, Amount: ex.Amount, Fee: ex.Fee, Reason: o.reason, RoundID: h.round.ID})
		e.last[o.symbol] = ex.Price
		return
	}
	ex, err := e.acct.Sell(ctx, id, o.symbol, f.Qty, f.Price)
	if err != nil {
		e.rejects["不可卖"]++
		return
	}
	h := e.hold[o.symbol]
	h.round.SellAmount += ex.Amount
	h.round.Fees += ex.Fee
	tr := Trade{Seq: len(e.trades) + 1, Symbol: o.symbol, Side: rules.SideSell, Time: t, Price: ex.Price, Qty: ex.Qty, Amount: ex.Amount, Fee: ex.Fee, Reason: o.reason, RoundID: h.round.ID}
	if e.acct.Book.Position(o.symbol).Quantity == 0 {
		r := h.round
		r.CloseTime = t
		r.PnL = round2(r.SellAmount - r.BuyAmount - r.Fees + r.Income)
		if r.BuyAmount > 0 {
			r.Return = r.PnL / r.BuyAmount
		}
		r.HoldDays = e.dayIdx - r.openIdx
		r.ExitReason = o.reason
		tr.PnL = r.PnL
		e.rounds = append(e.rounds, *r)
		delete(e.hold, o.symbol)
	}
	e.trades = append(e.trades, tr)
	e.last[o.symbol] = ex.Price
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// fillDaily 日线回测：用当日日线撮合前一交易日收盘生成的委托，开盘价成交。
func (e *engine) fillDaily(ctx context.Context, d time.Time) {
	syms := make([]string, 0, len(e.pending))
	for sym := range e.pending {
		syms = append(syms, sym)
	}
	sort.Strings(syms)
	for _, sym := range syms {
		o := e.pending[sym]
		delete(e.pending, sym)
		v := e.view[sym]
		if v == nil || v.today == nil {
			e.rejects["停牌或无成交"]++
			continue
		}
		b := v.today
		e.execute(ctx, o, sim.Quote{Open: b.Open, High: b.High, Low: b.Low, Volume: b.Volume}, at(d, 9, 30))
	}
}

// signalDaily 日线回测：15:00 用含当日的数据生成委托，下一交易日开盘撮合。
// 调用前已 RollDay，因此当日买入的股数在下一交易日开盘时可卖。
func (e *engine) signalDaily(ctx context.Context, d time.Time) {
	asOf := at(d, 15, 0)
	todayOf := func(sym string) *rules.Bar {
		v := e.view[sym]
		if v == nil || v.today == nil {
			return nil
		}
		b := v.today
		return &rules.Bar{Time: d, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume, Amount: b.Amount}
	}
	for _, sym := range e.heldSymbols() {
		if t := todayOf(sym); t != nil {
			e.decide(ctx, sym, e.snapshot(sym, asOf, t))
		}
	}
	for _, c := range e.rank(ctx, asOf, todayOf, 0) {
		if e.slotsUsed() >= e.cfg.MaxPositions {
			break
		}
		e.decide(ctx, c.sym, e.snapshot(c.sym, asOf, todayOf(c.sym)))
	}
}

// closeDay 按不复权收盘价估值，记权益点，然后进入下一交易日的 T+1 状态。
func (e *engine) closeDay(d time.Time) {
	for sym, v := range e.view {
		if v.today != nil {
			e.last[sym] = v.today.Close
		}
	}
	e.equity = append(e.equity, EquityPoint{Day: d, Equity: round2(e.equityNow()), Cash: round2(e.acct.Cash)})
	e.acct.Book.RollDay()
}

func (e *engine) result() *Result {
	res := &Result{
		Config:     e.cfg,
		Orders:     e.orders,
		Trades:     e.trades,
		Rounds:     e.rounds,
		RejectWhy:  e.rejects,
		Equity:     e.equity,
		Warnings:   e.warn,
		DataHash:   hex.EncodeToString(e.data0.Sum(nil)),
		DataCutoff: e.cutoff,
	}
	for _, n := range e.rejects {
		res.Rejects += n
	}
	e.fillBenchmark(res.Equity)
	for _, sym := range e.heldSymbols() {
		p := e.acct.Book.Position(sym)
		res.Open = append(res.Open, OpenPosition{
			Symbol: sym, Qty: p.Quantity, AvgCost: p.AvgCost, Last: e.last[sym],
			Value: round2(float64(p.Quantity) * e.last[sym]), HoldDays: e.dayIdx - e.hold[sym].openIdx,
		})
	}
	res.FillsHash = FillsHash(e.trades)
	return res
}

// fillBenchmark 把基准收盘价按首个交易日的前收归一到初始资金。基准缺某日时沿用上一日。
func (e *engine) fillBenchmark(eq []EquityPoint) {
	if len(e.bench) == 0 || len(eq) == 0 {
		return
	}
	closes := make(map[string]float64, len(e.bench))
	base := 0.0
	for _, b := range e.bench {
		closes[dayKey(b.Day)] = b.Close
		if b.Day.Before(eq[0].Day) {
			base = b.Close
		} else if base == 0 {
			base = b.Open
		}
	}
	if base <= 0 {
		return
	}
	last := base
	for i := range eq {
		if c, ok := closes[dayKey(eq[i].Day)]; ok {
			last = c
		}
		eq[i].Benchmark = round2(e.cfg.InitialCash * last / base)
	}
}

// FillsHash 是成交明细的指纹。重跑时数据哈希相同，它也必须相同。
func FillsHash(trades []Trade) string {
	h := sha256.New()
	for _, t := range trades {
		fmt.Fprintf(h, "%d|%s|%s|%d|%.4f|%d|%.4f|%.4f|%s\n", t.Seq, t.Symbol, t.Side, t.Time.Unix(), t.Price, t.Qty, t.Amount, t.Fee, t.Reason)
	}
	return hex.EncodeToString(h.Sum(nil))
}
