package market

import (
	"sort"
	"sync"
	"time"

	"server/pkg/ashare"
	"server/pkg/tradecal"
)

// StockInfo 是预热时从 stock_basic 和前一交易日结果里取到的静态信息。
type StockInfo struct {
	Symbol          string
	Name            string
	ST              bool
	Suspended       bool
	FloatShare      int64
	ListDate        time.Time
	Adj             float64 // 今日使用的后复权因子，取最近一根日线的因子
	PrevConsecutive int     // 前一交易日涨停连板数
	PrevAmount      float64
	MA5Volume       float64 // 前 5 日日均量
}

// EngineConfig 是引擎参数。
type EngineConfig struct {
	StaleAfter time.Duration
	BarGrace   time.Duration
	Rules      Rules
	PrevPhase  Phase
	Calendar   *tradecal.Calendar
}

type stockState struct {
	info     StockInfo
	ind      *Indicators
	agg      *Aggregator
	lim      *LimitTracker
	last     Snapshot
	hasLast  bool
	preMatch *Snapshot
	auction  *Snapshot
	values   Values
	stale    bool
	closed   bool // 当日日线已提交进指标状态
	recent   ring // 最近几根分钟线收盘，算急拉和指数 5 分钟涨跌
	surgeAt  time.Time
}

// 异动类别，与 M06 订阅的 market.alert 一致。
// staleShare：过期快照超过有行情股票的这一比例，整个情绪截面标 stale。个别股票偶发过期不影响全市场。
const staleShare = 0.05

const (
	AlertLimitUp = "limit_up"
	AlertBreak   = "break"
	AlertSurge   = "surge"
)

// Alert 是 market.alert 的载荷。
type Alert struct {
	Symbol string    `json:"symbol"`
	Kind   string    `json:"kind"`
	Time   time.Time `json:"time"`
	Price  float64   `json:"price"`
}

// Update 是一轮快照的处理结果。
type Update struct {
	Bars    []Bar
	Limits  []LimitEvent
	Alerts  []Alert
	Changed []string // 本轮因子有变化的代码
	Expired int
}

// Engine 保存全市场当天的实时状态。方法并发安全。
type Engine struct {
	mu     sync.RWMutex
	day    time.Time
	cfg    EngineConfig
	stocks map[string]*stockState
	index  *stockState
}

// NewEngine 建一个交易日的引擎。
func NewEngine(day time.Time, cfg EngineConfig) *Engine {
	if cfg.Calendar == nil {
		cfg.Calendar = tradecal.Default
	}
	if cfg.BarGrace <= 0 {
		cfg.BarGrace = 5 * time.Second
	}
	if cfg.Rules.Phases == nil {
		cfg.Rules = DefaultRules()
	}
	if cfg.Rules.SurgeMinutes <= 0 {
		cfg.Rules.SurgeMinutes = 5
	}
	e := &Engine{day: dateOf(day), cfg: cfg, stocks: map[string]*stockState{}}
	e.index = e.newState(StockInfo{Symbol: cfg.Rules.IndexSymbol, Adj: 1}, NewIndicators())
	return e
}

func (e *Engine) newState(info StockInfo, ind *Indicators) *stockState {
	return &stockState{info: info, ind: ind, agg: NewAggregator(info.Symbol), recent: newRing(e.cfg.Rules.SurgeMinutes)}
}

// Day 返回引擎所属交易日。
func (e *Engine) Day() time.Time { return e.day }

// Load 预热一只股票：history 是截至前一交易日的日线，按时间升序。
func (e *Engine) Load(info StockInfo, history []DayBar) {
	ind := NewIndicators()
	for _, b := range history {
		ind.Push(b)
	}
	if info.Adj <= 0 {
		info.Adj = 1
	}
	e.mu.Lock()
	e.stocks[info.Symbol] = e.newState(info, ind)
	e.mu.Unlock()
}

func (e *Engine) state(sym string) *stockState {
	st := e.stocks[sym]
	if st == nil {
		st = e.newState(StockInfo{Symbol: sym, Adj: 1}, NewIndicators())
		e.stocks[sym] = st
	}
	return st
}

// Process 处理一轮快照。过期快照只计数，不参与计算。指数只用来算市场状态；基金、债券等非 A 股忽略。
func (e *Engine) Process(snaps []Snapshot, now time.Time) Update {
	e.mu.Lock()
	defer e.mu.Unlock()
	var up Update
	for i := range snaps {
		s := snaps[i]
		if !sameDay(s.Time, e.day) {
			continue
		}
		var st *stockState
		switch {
		case s.Symbol == e.cfg.Rules.IndexSymbol:
			st = e.index
		case ashare.IsAStock(s.Symbol):
			st = e.state(s.Symbol)
		default:
			continue
		}
		if s.Expired(now, e.cfg.StaleAfter) {
			st.stale = true
			up.Expired++
			continue
		}
		st.stale = false
		if st.hasLast && !s.Time.After(st.last.Time) && s.Volume == st.last.Volume {
			continue
		}
		closed := st.agg.Push(s)
		for _, b := range closed {
			st.recent.push(b.Close)
		}
		st.last, st.hasLast = s, true
		if st == e.index {
			continue
		}
		up.Bars = append(up.Bars, closed...)
		e.observeAuction(st, s)
		if st.lim == nil && s.PreClose > 0 {
			st.lim = e.newTracker(st.info, s.PreClose)
		}
		if st.lim != nil {
			evs := st.lim.Push(s)
			up.Limits = append(up.Limits, evs...)
			for _, ev := range evs {
				if ev.Direction != DirUp {
					continue
				}
				kind := AlertLimitUp
				if ev.Kind == EventBreak {
					kind = AlertBreak
				}
				up.Alerts = append(up.Alerts, Alert{Symbol: s.Symbol, Kind: kind, Time: s.Time, Price: s.Last})
			}
		}
		if a, ok := e.surge(st, s); ok {
			up.Alerts = append(up.Alerts, a)
		}
		if st.closed {
			continue
		}
		st.values = e.preview(st)
		up.Changed = append(up.Changed, s.Symbol)
	}
	return up
}

// surge：现价相对 SurgeMinutes 根之前的分钟收盘涨幅达阈值，同一只股票一个窗口内只报一次。
func (e *Engine) surge(st *stockState, s Snapshot) (Alert, bool) {
	n := e.cfg.Rules.SurgeMinutes
	if e.cfg.Rules.SurgePct <= 0 || st.recent.n < n || s.Last <= 0 {
		return Alert{}, false
	}
	ref := st.recent.back(n - 1)
	if ref <= 0 || (s.Last-ref)/ref*100 < e.cfg.Rules.SurgePct {
		return Alert{}, false
	}
	if !st.surgeAt.IsZero() && s.Time.Sub(st.surgeAt) < time.Duration(n)*time.Minute {
		return Alert{}, false
	}
	st.surgeAt = s.Time
	return Alert{Symbol: s.Symbol, Kind: AlertSurge, Time: s.Time, Price: s.Last}, true
}

// State 返回给 M08 的市场状态。
func (e *Engine) State(asOf time.Time) State {
	s := e.Sentiment(asOf)
	e.mu.RLock()
	defer e.mu.RUnlock()
	var pct, pct5 float64
	idx := e.index
	if idx.hasLast && idx.last.PreClose > 0 {
		pct = (idx.last.Last - idx.last.PreClose) / idx.last.PreClose * 100
		if n := e.cfg.Rules.SurgeMinutes; idx.recent.n >= n {
			if ref := idx.recent.back(n - 1); ref > 0 {
				pct5 = (idx.last.Last - ref) / ref * 100
			}
		}
	}
	return State{
		TradeDate: e.day, AsOf: asOf, Phase: s.Phase,
		RiskLevel: e.cfg.Rules.RiskLevel(s, pct, pct5),
		IndexPct:  pct, Index5m: pct5, BrokenRate: s.BrokenRate,
		ScoreCoef: s.ScoreCoef, Position: s.PositionScale,
		Stale: s.Stale || !idx.hasLast || idx.stale,
	}
}

func (e *Engine) observeAuction(st *stockState, s Snapshot) {
	d := s.Time.In(tradecal.Shanghai())
	m := d.Hour()*60 + d.Minute()
	switch {
	case m >= 9*60+20 && m < 9*60+25:
		cp := s
		st.preMatch = &cp
	case m >= 9*60+25 && m < 9*60+30 && st.auction == nil && s.Volume > 0:
		cp := s
		st.auction = &cp
	}
}

func (e *Engine) newTracker(info StockInfo, preClose float64) *LimitTracker {
	ratio, err := ashare.RatioOn(info.Symbol, info.ST, e.day)
	if err != nil {
		ratio = 0
	}
	noLimit := NoLimit(e.cfg.Calendar, info.ListDate, e.day)
	return NewLimitTracker(info.Symbol, e.day, preClose, ratio, info.PrevConsecutive, noLimit)
}

func (e *Engine) todayBar(st *stockState) DayBar {
	s := st.last
	open := s.Open
	if open <= 0 {
		open = s.Last
	}
	return DayBar{
		Symbol: st.info.Symbol, Time: e.day, Open: open, High: s.High, Low: s.Low, Close: s.Last,
		PreClose: s.PreClose, Volume: s.Volume, Amount: s.Amount,
		Adj: st.info.Adj, FloatShare: st.info.FloatShare,
	}
}

func (e *Engine) preview(st *stockState) Values {
	minutes := 1
	if slot, ok := Slot(st.last.Time); ok {
		minutes = SlotIndex(slot)
	}
	v := st.ind.Preview(e.todayBar(st), minutes).Rebase(st.info.Adj)
	e.addLimitValues(st, v)
	return v
}

func (e *Engine) addLimitValues(st *stockState, v Values) {
	if st.lim == nil || !st.lim.Enabled() {
		return
	}
	upPx, downPx := st.lim.Prices()
	v["limit_up_price"], v["limit_down_price"] = upPx, downPx
	v["up_sealed"], v["up_broken"], v["down_sealed"] = 0, 0, 0
	for _, b := range st.lim.Boards() {
		switch {
		case b.Direction == DirUp && b.Status == StatusSealed:
			v["up_sealed"] = 1
			v["consecutive"] = float64(b.Consecutive)
		case b.Direction == DirUp:
			v["up_broken"] = 1
		case b.Status == StatusSealed:
			v["down_sealed"] = 1
		}
		if b.Direction == DirUp {
			v["open_count"] = float64(b.OpenCount)
			v["seal_amount"] = b.SealAmount
			if !b.FirstSealAt.IsZero() {
				v["first_seal_min"] = float64(SlotIndexAt(b.FirstSealAt))
			}
		}
	}
}

// SlotIndexAt 返回某时刻是当天第几个交易分钟；开盘前为 0。
func SlotIndexAt(t time.Time) int {
	slot, ok := Slot(t)
	if !ok {
		return 0
	}
	return SlotIndex(slot)
}

// Flush 封口到期的分钟线。
func (e *Engine) Flush(now time.Time) []Bar {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Bar
	for _, st := range e.stocks {
		bars := st.agg.Flush(now, e.cfg.BarGrace)
		for _, b := range bars {
			st.recent.push(b.Close)
		}
		out = append(out, bars...)
	}
	for _, b := range e.index.agg.Flush(now, e.cfg.BarGrace) {
		e.index.recent.push(b.Close)
	}
	return out
}

// Factors 返回某只股票的实时因子副本。
func (e *Engine) Factors(sym string) (Values, bool, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st := e.stocks[sym]
	if st == nil || st.values == nil {
		return nil, false, false
	}
	return copyValues(st.values), st.stale, true
}

// FactorRow 是截面里的一行。
type FactorRow struct {
	Symbol string    `json:"symbol"`
	AsOf   time.Time `json:"as_of"`
	Values Values    `json:"values"`
	Stale  bool      `json:"stale"`
}

// Cross 返回全市场实时截面，按代码排序。symbols 为空时返回全部。
func (e *Engine) Cross(symbols []string) []FactorRow {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []FactorRow
	add := func(st *stockState) {
		if st == nil || st.values == nil {
			return
		}
		out = append(out, FactorRow{Symbol: st.info.Symbol, AsOf: st.last.Time, Values: copyValues(st.values), Stale: st.stale})
	}
	if len(symbols) > 0 {
		for _, s := range symbols {
			add(e.stocks[s])
		}
	} else {
		for _, st := range e.stocks {
			add(st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

// Boards 返回全市场当前涨跌停记录。
func (e *Engine) Boards() []LimitBoard {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []LimitBoard
	for _, st := range e.stocks {
		if st.lim != nil {
			out = append(out, st.lim.Boards()...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Symbol != out[j].Symbol {
			return out[i].Symbol < out[j].Symbol
		}
		return out[i].Direction < out[j].Direction
	})
	return out
}

func (e *Engine) stockDays() []StockDay {
	var out []StockDay
	for _, st := range e.stocks {
		if !st.hasLast {
			continue
		}
		s := st.last
		d := StockDay{
			Symbol:     st.info.Symbol,
			Amount:     s.Amount,
			Suspended:  st.info.Suspended || s.Volume == 0,
			PrevSealed: st.info.PrevConsecutive > 0,
		}
		if s.PreClose > 0 {
			d.PctChg = (s.Last - s.PreClose) / s.PreClose * 100
		}
		if st.lim == nil || !st.lim.Enabled() {
			d.NoLimit = true
		} else {
			upPx, _ := st.lim.Prices()
			d.OneWord = s.Open >= upPx-priceEps && s.Low >= upPx-priceEps && s.Last >= upPx-priceEps
			for _, b := range st.lim.Boards() {
				if b.Direction == DirUp {
					d.UpStatus = b.Status
					d.Consecutive = b.Consecutive
				} else {
					d.DownStatus = b.Status
				}
			}
		}
		out = append(out, d)
	}
	return out
}

// Sentiment 计算当前情绪截面。
func (e *Engine) Sentiment(asOf time.Time) Sentiment {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s := ComputeSentiment(e.stockDays())
	s.TradeDate, s.AsOf = e.day, asOf
	seen, stale := 0, 0
	for _, st := range e.stocks {
		if st.hasLast || st.stale {
			seen++
		}
		if st.stale {
			stale++
		}
	}
	s.Stale = seen == 0 || float64(stale) > staleShare*float64(seen)
	return e.cfg.Rules.Classify(s, e.cfg.PrevPhase)
}

// MemberDays 给板块热度用的个股状态。
func (e *Engine) MemberDays() map[string]MemberDay {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[string]MemberDay, len(e.stocks))
	for sym, st := range e.stocks {
		if !st.hasLast {
			continue
		}
		s := st.last
		m := MemberDay{Symbol: sym, Amount: s.Amount, Suspended: st.info.Suspended || s.Volume == 0}
		if s.PreClose > 0 {
			m.PctChg = (s.Last - s.PreClose) / s.PreClose * 100
		}
		if st.lim != nil {
			for _, b := range st.lim.Boards() {
				if b.Direction == DirUp {
					m.UpStatus, m.Consecutive, m.FirstSealAt, m.SealAmount = b.Status, b.Consecutive, b.FirstSealAt, b.SealAmount
				}
			}
		}
		out[sym] = m
	}
	return out
}

// AuctionCross 返回竞价因子截面。没有 09:25 快照的股票不出现。
func (e *Engine) AuctionCross() []FactorRow {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []FactorRow
	for sym, st := range e.stocks {
		if st.auction == nil {
			continue
		}
		in := AuctionInput{
			Open: *st.auction, PreMatch: st.preMatch, MA5Volume: st.info.MA5Volume,
			FloatShare: st.info.FloatShare, PrevAmount: st.info.PrevAmount,
		}
		if st.lim != nil && st.lim.Enabled() {
			in.UpLimit, _ = st.lim.Prices()
		}
		out = append(out, FactorRow{Symbol: sym, AsOf: st.auction.Time, Values: AuctionFactors(in)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

// CloseDay 收盘定稿：用最后一条快照合成日线，提交进指标状态，返回日线和收盘因子。
// 同一天只应调用一次；再次调用返回同样的结果而不重复提交。
func (e *Engine) CloseDay() ([]DayBar, []FactorRow) {
	e.mu.Lock()
	defer e.mu.Unlock()
	asOf := closeTime(e.day)
	var bars []DayBar
	var rows []FactorRow
	syms := make([]string, 0, len(e.stocks))
	for sym := range e.stocks {
		syms = append(syms, sym)
	}
	sort.Strings(syms)
	for _, sym := range syms {
		st := e.stocks[sym]
		if !st.hasLast || st.last.Volume == 0 {
			continue
		}
		b := e.todayBar(st)
		var v Values
		if st.closed {
			v = st.values
		} else {
			v = st.ind.Push(b).Rebase(st.info.Adj)
			e.addLimitValues(st, v)
			st.values = v
			st.closed = true
		}
		bars = append(bars, b)
		rows = append(rows, FactorRow{Symbol: sym, AsOf: asOf, Values: copyValues(v), Stale: st.stale})
	}
	return bars, rows
}

func copyValues(v Values) Values {
	out := make(Values, len(v))
	for k, x := range v {
		out[k] = x
	}
	return out
}
