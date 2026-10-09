package biz

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"server/pkg/events"
	"server/pkg/risk"
	"server/pkg/symbol"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
)

const (
	reservationTTL   = 60 * time.Second
	reservationGrace = 2 * time.Second
	exitCooldown     = 60 * time.Second
	quoteCacheTTL    = time.Second
	refRefresh       = 10 * time.Minute
)

// ErrInvalid 是参数错误，service 层转成 400。
var ErrInvalid = errors.New("invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

type reservation struct {
	risk.Reservation
	account risk.AccountType
}

type cachedQuote struct {
	snap    Snapshot
	fetched time.Time
}

// Engine 持有风控的全部内存状态。Check 串行执行，保证同一笔资金不会被两张单同时用掉。
type Engine struct {
	repo   Repo
	quotes QuoteSource
	audit  Auditor
	pub    Publisher
	log    *log.Helper

	now     func() time.Time
	session func(time.Time) tradecal.Session
	cal     *tradecal.Calendar

	ready   atomic.Bool
	checkMu sync.Mutex
	killMu  sync.Mutex
	mu      sync.RWMutex

	params      risk.Params
	mode        risk.Mode
	kill        risk.KillState
	killUnsaved bool
	accounts    map[risk.AccountType]risk.Account
	cache       map[string]cachedQuote
	ref         map[string]RefInfo
	refAt       time.Time
	market      risk.Market

	lastQuoteAt  time.Time
	tradingSince time.Time

	reservations map[string]reservation
	day          string
	buys         map[risk.AccountType]int
	rates        map[risk.AccountType]*risk.Rate
	breakers     map[risk.AccountType]string
	marketBrk    string
	highs        map[string]float64
	news         map[string]string
	exits        map[string]time.Time
	checks       []risk.CheckMark
	rejectAlert  time.Time
}

func NewEngine(repo Repo, quotes QuoteSource, audit Auditor, pub Publisher, logger log.Logger) *Engine {
	e := &Engine{
		repo: repo, quotes: quotes, audit: audit, pub: pub,
		log:          log.NewHelper(log.With(logger, "module", "risk/biz")),
		now:          time.Now,
		cal:          tradecal.Default,
		params:       risk.Defaults(),
		accounts:     map[risk.AccountType]risk.Account{},
		cache:        map[string]cachedQuote{},
		ref:          map[string]RefInfo{},
		reservations: map[string]reservation{},
		buys:         map[risk.AccountType]int{},
		rates:        map[risk.AccountType]*risk.Rate{risk.SIM: {}, risk.LIVE: {}},
		breakers:     map[risk.AccountType]string{},
		highs:        map[string]float64{},
		news:         map[string]string{},
		exits:        map[string]time.Time{},
	}
	e.session = func(t time.Time) tradecal.Session {
		s, err := e.cal.SessionAt(t)
		if err != nil {
			return tradecal.Session{Phase: tradecal.Closed}
		}
		return s
	}
	return e
}

// Start 从库里恢复参数、Kill Switch 和模式。Kill Switch 已触发时保持触发。
func (e *Engine) Start(ctx context.Context) error {
	rows, err := e.repo.LoadParams(ctx)
	if err != nil {
		return fmt.Errorf("risk: load params: %w", err)
	}
	p, errs := risk.FromConfig(rows)
	for _, err := range errs {
		e.log.Warnf("ignore bad param: %v", err)
	}
	kill, err := e.repo.LastKill(ctx)
	if err != nil {
		return fmt.Errorf("risk: load kill switch: %w", err)
	}
	mode, ok, err := e.repo.LastMode(ctx)
	if err != nil {
		return fmt.Errorf("risk: load mode: %w", err)
	}
	if !ok {
		mode = risk.L0
	}
	if kill.Active {
		mode = risk.L0
	}
	if mode >= risk.L2 && !p.LiveAdmitted {
		mode = risk.L1
	}
	now := e.now().In(tradecal.Shanghai())
	day, err := e.repo.RestoreDay(ctx, time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tradecal.Shanghai()))
	if err != nil {
		return fmt.Errorf("risk: restore day: %w", err)
	}
	e.mu.Lock()
	e.params, e.kill, e.mode = p, kill, mode
	e.day = e.dayKey(now)
	for a, why := range day.Breakers {
		e.breakers[a] = why
	}
	for a, n := range day.Buys {
		e.buys[a] = n
	}
	e.mu.Unlock()
	e.refreshRef(ctx)
	e.ready.Store(true)
	e.log.Infof("risk started: mode=%s kill_switch=%v", mode, kill.Active)
	return nil
}

func (e *Engine) dayKey(t time.Time) string {
	return t.In(tradecal.Shanghai()).Format("2006-01-02")
}

func normSymbol(raw string) (string, bool) {
	s, err := symbol.Parse(raw)
	if err != nil {
		return "", false
	}
	return s.Tongdaxin(), true
}

// Check 是事前校验。ctx 到期视为拒绝。
func (e *Engine) Check(ctx context.Context, o risk.Order) (risk.Decision, time.Duration) {
	start := time.Now()
	if s, ok := normSymbol(o.Symbol); ok {
		o.Symbol = s
	}
	snap, haveSnap := e.quote(ctx, o.Symbol)

	e.checkMu.Lock()
	defer e.checkMu.Unlock()
	now := e.now()
	in, p := e.input(o, now, snap, haveSnap)
	d := risk.Check(in, p)
	if !e.ready.Load() {
		d = risk.Reject(risk.RuleInvalid, "风控尚未从库里恢复状态")
	}
	if ctx.Err() != nil {
		d = risk.Reject(risk.RuleInvalid, "风控超时")
	}
	lat := time.Since(start)
	if !e.audit.Submit(AuditRecord{Decision: d, Input: in, Latency: lat}) {
		e.log.Errorf("audit queue full, reject %s", o.ClientID)
		return risk.Reject(risk.RuleInvalid, "审计队列已满"), lat
	}
	if d.Approved {
		e.commit(in, d, now)
	}
	e.noteCheck(now, d.Approved)
	return d, lat
}

func (e *Engine) noteCheck(at time.Time, approved bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.checks = append(e.checks, risk.CheckMark{At: at, Approved: approved})
	if len(e.checks) > 10000 {
		e.checks = e.checks[len(e.checks)-10000:]
	}
}

func (e *Engine) input(o risk.Order, now time.Time, snap Snapshot, haveSnap bool) (risk.Input, risk.Params) {
	e.rollDay(now)
	e.mu.RLock()
	defer e.mu.RUnlock()
	acct := e.accounts[o.Account]
	if acct.Holdings == nil {
		acct.Holdings = map[string]risk.Holding{}
	}
	if o.Sector == "" {
		o.Sector = e.ref[o.Symbol].Industry
	}
	in := risk.Input{
		Now:        now,
		Session:    e.session(now),
		Mode:       e.mode,
		KillSwitch: e.kill.Active,
		Breaker:    e.breakers[o.Account],
		Order:      o,
		Account:    acct,
		Market:     e.marketView(),
	}
	if haveSnap {
		q := e.toQuote(snap, now)
		in.Quote = &q
	}
	if r := e.rates[o.Account]; r != nil {
		in.Counters.LastSecond, in.Counters.Today = r.Counts(now)
	}
	in.Counters.BuysToday = e.buys[o.Account]
	for _, r := range e.reservations {
		if r.account == o.Account {
			in.Reservations = append(in.Reservations, r.Reservation)
		}
	}
	sort.Slice(in.Reservations, func(i, j int) bool { return in.Reservations[i].At.Before(in.Reservations[j].At) })
	return in, e.params
}

// marketView 把人工风险度下限叠加到 M02 的风险度上。调用方持有读锁。
func (e *Engine) marketView() risk.Market {
	m := e.market
	if e.params.RiskLevel > m.RiskLevel {
		m.RiskLevel = e.params.RiskLevel
	}
	return m
}

func (e *Engine) toQuote(s Snapshot, now time.Time) risk.Quote {
	ref := e.ref[s.Symbol]
	q := risk.Quote{
		Symbol: s.Symbol, Last: s.Last, PrevClose: s.PreClose, Bid1: s.Bid1, Ask1: s.Ask1,
		Amount: s.Amount, PrevAmount: ref.PrevAmount, ST: ref.ST, Suspended: ref.Suspended,
		ListedDays: e.listedDays(ref.ListDate, now),
		Time:       s.AsOf,
	}
	if q.Time.IsZero() {
		q.Time = s.Time
	}
	if s.Stale {
		q.Time = time.Time{}
	}
	return q
}

// listedDays 数上市以来的交易日（含上市当日），只数到 30；不知道上市日返回 0。
func (e *Engine) listedDays(list, now time.Time) int {
	if list.IsZero() {
		return 0
	}
	if now.Sub(list) > 60*24*time.Hour {
		return 1000
	}
	n := 0
	for d := list; !d.After(now) && n <= 30; d = d.AddDate(0, 0, 1) {
		if ok, err := e.cal.Open(d); err == nil && ok {
			n++
		}
	}
	if n == 0 {
		n = 1
	}
	return n
}

func (e *Engine) commit(in risk.Input, d risk.Decision, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	o := in.Order
	if r := e.rates[o.Account]; r != nil {
		r.Record(now)
	}
	if o.Side != risk.Buy {
		return
	}
	e.buys[o.Account]++
	e.reservations[o.ClientID] = reservation{
		Reservation: risk.Reservation{
			ClientID: o.ClientID, Symbol: o.Symbol, Sector: o.Sector,
			Amount: o.Price * float64(d.Volume), At: now,
		},
		account: o.Account,
	}
}

// quote 取一只股票的快照：内存里 1 秒内的直接用，否则读 Redis。
func (e *Engine) quote(ctx context.Context, sym string) (Snapshot, bool) {
	e.mu.RLock()
	c, ok := e.cache[sym]
	e.mu.RUnlock()
	if ok && e.now().Sub(c.fetched) < quoteCacheTTL {
		return c.snap, true
	}
	got, err := e.quotes.Quotes(ctx, []string{sym})
	if err != nil {
		e.log.Warnf("read quote %s: %v", sym, err)
		return c.snap, ok
	}
	s, found := got[sym]
	if !found {
		return Snapshot{}, false
	}
	e.storeQuotes(map[string]Snapshot{sym: s})
	return s, true
}

func (e *Engine) storeQuotes(m map[string]Snapshot) {
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	for sym, s := range m {
		e.cache[sym] = cachedQuote{snap: s, fetched: now}
		if !s.Stale && s.AsOf.After(e.lastQuoteAt) {
			e.lastQuoteAt = s.AsOf
		}
	}
}

// rollDay 在换日时清掉当日计数、熔断锁存、利空标记和盯盘冷却。
func (e *Engine) rollDay(now time.Time) {
	key := e.dayKey(now)
	e.mu.Lock()
	defer e.mu.Unlock()
	if key == e.day {
		return
	}
	e.day = key
	e.buys = map[risk.AccountType]int{}
	e.breakers = map[risk.AccountType]string{}
	e.news = map[string]string{}
	e.exits = map[string]time.Time{}
}

// OnAccount 收到账户快照：替换内存账本，清掉已进入快照的预留，检查当日亏损熔断。
func (e *Engine) OnAccount(ctx context.Context, p AccountPayload) {
	if p.AccountType != risk.SIM && p.AccountType != risk.LIVE {
		e.log.Warnf("account snapshot with bad type %q", p.AccountType)
		return
	}
	acct := risk.Account{
		Type: p.AccountType, Equity: p.Equity, Available: p.Available, DayStartEquity: p.DayStartEquity,
		Holdings: make(map[string]risk.Holding, len(p.Holdings)), AsOf: p.AsOf,
	}
	for _, h := range p.Holdings {
		if s, ok := normSymbol(h.Symbol); ok {
			h.Symbol = s
			acct.Holdings[s] = h
		}
	}
	for _, o := range p.OpenOrders {
		if s, ok := normSymbol(o.Symbol); ok {
			o.Symbol = s
			acct.Open = append(acct.Open, o)
		}
	}
	now := e.now()
	e.rollDay(now)
	e.mu.Lock()
	if prev, ok := e.accounts[p.AccountType]; ok && prev.AsOf.After(p.AsOf) {
		e.mu.Unlock()
		return
	}
	for s, h := range acct.Holdings {
		if h.Sector == "" {
			h.Sector = e.ref[s].Industry
			acct.Holdings[s] = h
		}
	}
	for i, o := range acct.Open {
		if o.Sector == "" {
			acct.Open[i].Sector = e.ref[o.Symbol].Industry
		}
	}
	e.accounts[p.AccountType] = acct
	for id, r := range e.reservations {
		if r.account == p.AccountType && r.At.Add(reservationGrace).Before(p.AsOf) {
			delete(e.reservations, id)
		}
	}
	latch := ""
	if e.breakers[p.AccountType] == "" && risk.DailyLossHit(acct, e.params) {
		latch = fmt.Sprintf("当日亏损 %.2f%%", (acct.Equity-acct.DayStartEquity)/acct.DayStartEquity*100)
		e.breakers[p.AccountType] = latch
	}
	e.mu.Unlock()
	if latch != "" {
		e.log.Warnf("breaker latched for %s: %s", p.AccountType, latch)
		if err := e.repo.SaveBreaker(ctx, p.AccountType, true, latch); err != nil {
			e.log.Errorf("save breaker: %v", err)
		}
	}
}

// OnOrderAction 把撤单计入速度上限。申报在 Check 放行时已经计过。
func (e *Engine) OnOrderAction(p OrderActionPayload) {
	if p.Action != "cancel" {
		return
	}
	e.mu.RLock()
	r := e.rates[p.AccountType]
	e.mu.RUnlock()
	if r != nil {
		r.Record(e.now())
	}
}

// OnMarketState 收到 M02 的市场状态。市场类熔断变化时落库并告警。
func (e *Engine) OnMarketState(ctx context.Context, p StatePayload) {
	if p.Stale {
		return
	}
	e.mu.Lock()
	e.market = risk.Market{
		Phase: strings.ToUpper(p.Phase), PositionScale: p.Position, RiskLevel: p.RiskLevel,
		IndexChange: p.IndexPct / 100, Index5mChange: p.Index5m / 100, BurstRate: p.BrokenRate, Time: p.AsOf,
	}
	why := risk.MarketBreaker(e.marketView(), e.params)
	changed := why != e.marketBrk
	e.marketBrk = why
	e.mu.Unlock()
	if !changed {
		return
	}
	reason := why
	if reason == "" {
		reason = "市场类熔断解除"
	}
	if err := e.repo.SaveBreaker(ctx, "", why != "", reason); err != nil {
		e.log.Errorf("save market breaker: %v", err)
	}
}

// OnIntelAlert 把情感低于阈值的告警标为突发利空，当日有效。
func (e *Engine) OnIntelAlert(p IntelAlertPayload) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p.Sentiment > e.params.BadNewsSentiment {
		return
	}
	for _, s := range p.Stocks {
		if sym, ok := normSymbol(s); ok {
			e.news[sym] = p.Title
		}
	}
}

// OnSnapshot 在每轮快照后刷新持仓标的的行情并盯盘。兜底轮询时 meta 为零值。
func (e *Engine) OnSnapshot(ctx context.Context, meta SnapshotMetaPayload) {
	e.mu.Lock()
	if meta.Count > 0 && !meta.Stale && meta.AsOf.After(e.lastQuoteAt) {
		e.lastQuoteAt = meta.AsOf
	}
	e.mu.Unlock()
	e.mu.RLock()
	set := map[string]struct{}{}
	for _, a := range e.accounts {
		for sym, h := range a.Holdings {
			if h.Quantity > 0 {
				set[sym] = struct{}{}
			}
		}
	}
	e.mu.RUnlock()
	if len(set) == 0 {
		return
	}
	syms := make([]string, 0, len(set))
	for s := range set {
		syms = append(syms, s)
	}
	got, err := e.quotes.Quotes(ctx, syms)
	if err != nil {
		e.log.Warnf("read held quotes: %v", err)
		return
	}
	e.storeQuotes(got)
	e.watch(ctx, got)
}

func (e *Engine) watch(ctx context.Context, snaps map[string]Snapshot) {
	now := e.now()
	e.rollDay(now)
	var out []ExitPayload
	e.mu.Lock()
	sess := e.session(now)
	for _, a := range e.accounts {
		for sym, h := range a.Holdings {
			s, ok := snaps[sym]
			if !ok || h.Quantity <= 0 {
				continue
			}
			key := string(a.Type) + "|" + sym
			if s.Last > e.highs[key] {
				e.highs[key] = s.Last
			}
			q := e.toQuote(s, now)
			w := risk.WatchInput{
				Input:        risk.Input{Now: now, Session: sess, Account: a, Quote: &q, Market: e.marketView()},
				Holding:      h,
				High:         e.highs[key],
				NegativeNews: e.news[sym] != "",
			}
			ex, hit := risk.WatchPosition(w, e.params)
			if !hit || now.Sub(e.exits[key]) < exitCooldown {
				continue
			}
			e.exits[key] = now
			out = append(out, ExitPayload{ExitID: uuid.NewString(), Exit: ex, At: now})
		}
	}
	e.mu.Unlock()
	for _, ex := range out {
		e.log.Infof("exit %s %s %s x%d @%.2f: %s", ex.Account, ex.Symbol, ex.Trigger, ex.Volume, ex.Price, ex.Note)
		if err := e.pub.Publish(ctx, events.SubjectRiskExit, ex); err != nil {
			e.log.Errorf("publish exit %s: %v", ex.Symbol, err)
			e.mu.Lock()
			delete(e.exits, string(ex.Account)+"|"+ex.Symbol)
			e.mu.Unlock()
		}
	}
}

// Tick 每秒调用：换日、过期预留、刷新个股信息、检查行情中断。
func (e *Engine) Tick(ctx context.Context) {
	now := e.now()
	e.rollDay(now)
	sess := e.session(now)
	e.mu.Lock()
	for id, r := range e.reservations {
		if now.Sub(r.At) > reservationTTL {
			delete(e.reservations, id)
		}
	}
	refDue := now.Sub(e.refAt) > refRefresh
	down := false
	var since time.Duration
	if !sess.Trading {
		e.tradingSince = time.Time{}
	} else {
		if e.tradingSince.IsZero() {
			e.tradingSince = now
		}
		base := e.lastQuoteAt
		if e.tradingSince.After(base) {
			base = e.tradingSince
		}
		since = now.Sub(base)
		down = !e.kill.Active && since > time.Duration(e.params.MarketDownSec)*time.Second && e.hasPositions()
	}
	unsaved := e.killUnsaved
	_, _, rejectAlert := risk.RejectAlert(e.checks, now, e.params, e.rejectAlert)
	e.mu.Unlock()
	if unsaved {
		e.retryKillSave(ctx)
	}
	if rejectAlert {
		e.fireRejectAlert(ctx, now)
	}
	if refDue {
		e.refreshRef(ctx)
	}
	if down {
		reason := fmt.Sprintf("交易时段 %d 秒没有行情且有持仓", int(since.Seconds()))
		if _, err := e.TriggerKill(ctx, risk.KillFromMarket, reason, ""); err != nil {
			e.log.Errorf("trigger kill switch: %v", err)
		}
	}
}

func (e *Engine) fireRejectAlert(ctx context.Context, now time.Time) {
	e.mu.RLock()
	n, rate, _ := risk.RejectAlert(e.checks, now, e.params, time.Time{})
	window := int(e.params.RejectAlertWindow.Seconds())
	e.mu.RUnlock()
	msg := fmt.Sprintf("近 %d 秒校验 %d 笔，拒绝率 %.0f%%", window, n, rate*100)
	if err := e.repo.SaveAlert(ctx, msg); err != nil {
		e.log.Errorf("save reject alert: %v", err)
		return
	}
	e.log.Warnf("reject alert: %s", msg)
	e.mu.Lock()
	e.rejectAlert = now
	e.mu.Unlock()
}

// hasPositions 调用方持有锁。
func (e *Engine) hasPositions() bool {
	for _, a := range e.accounts {
		for _, h := range a.Holdings {
			if h.Quantity > 0 {
				return true
			}
		}
	}
	return false
}

func (e *Engine) refreshRef(ctx context.Context) {
	day, err := e.cal.LastClosedDay(e.now())
	if err != nil {
		e.log.Warnf("ref data: %v", err)
		day = e.now()
	}
	ref, err := e.repo.RefData(ctx, day)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refAt = e.now()
	if err != nil {
		e.log.Warnf("load ref data: %v", err)
		return
	}
	e.ref = ref
}

// TriggerKill 触发 Kill Switch：先在内存里停止放行，再落库发事件。
func (e *Engine) TriggerKill(ctx context.Context, source, reason, operator string) (State, error) {
	if strings.TrimSpace(source) == "" {
		return State{}, invalid("source is required")
	}
	if source == risk.KillFromClient && strings.TrimSpace(operator) == "" {
		return State{}, invalid("operator is required")
	}
	e.killMu.Lock()
	defer e.killMu.Unlock()
	e.mu.Lock()
	next, changed := e.kill.Trigger(source, reason, operator, e.now())
	if changed {
		e.kill, e.mode = next, risk.L0
	}
	pending := changed || e.killUnsaved
	e.mu.Unlock()
	if changed {
		e.log.Warnf("KILL SWITCH from %s: %s", source, reason)
	}
	if pending {
		if err := e.saveKill(ctx, next); err != nil {
			return e.State(), err
		}
	}
	return e.State(), nil
}

// saveKill 落库触发状态；失败时记下，由 Tick 和下一次触发重试。调用方持有 killMu。
func (e *Engine) saveKill(ctx context.Context, k risk.KillState) error {
	err := e.repo.SaveKill(ctx, k, risk.L0)
	e.mu.Lock()
	e.killUnsaved = err != nil
	e.mu.Unlock()
	if err != nil {
		return fmt.Errorf("kill switch is active in memory but not saved: %w", err)
	}
	return nil
}

func (e *Engine) retryKillSave(ctx context.Context) {
	e.killMu.Lock()
	defer e.killMu.Unlock()
	e.mu.RLock()
	k, pending := e.kill, e.killUnsaved && e.kill.Active
	e.mu.RUnlock()
	if !pending {
		return
	}
	if err := e.saveKill(ctx, k); err != nil {
		e.log.Errorf("%v", err)
	}
}

// ResetKill 人工恢复 Kill Switch。先落库成功再改内存；模式保持 L0。
func (e *Engine) ResetKill(ctx context.Context, operator, confirm, reason string) (State, error) {
	e.killMu.Lock()
	defer e.killMu.Unlock()
	e.mu.RLock()
	next, err := e.kill.Reset(operator, confirm, reason, e.now())
	e.mu.RUnlock()
	if err != nil {
		return State{}, invalid("%v", err)
	}
	if err := e.repo.SaveKill(ctx, next, risk.L0); err != nil {
		return State{}, err
	}
	e.mu.Lock()
	e.kill, e.mode, e.killUnsaved = next, risk.L0, false
	e.mu.Unlock()
	e.log.Warnf("kill switch reset by %s", operator)
	return e.State(), nil
}

// SetMode 切换模式。必须有操作人；Kill Switch 触发时只能 L0；L2 以上要求实盘准入。
func (e *Engine) SetMode(ctx context.Context, raw, operator, reason string) (State, error) {
	m, err := risk.ParseMode(raw)
	if err != nil {
		return State{}, invalid("%v", err)
	}
	if strings.TrimSpace(operator) == "" {
		return State{}, invalid("operator is required")
	}
	e.mu.RLock()
	err = risk.ModeAllowed(m, e.kill, e.params)
	e.mu.RUnlock()
	if err != nil {
		return State{}, invalid("%v", err)
	}
	if err := e.repo.SaveMode(ctx, m, operator, reason); err != nil {
		return State{}, err
	}
	e.mu.Lock()
	e.mode = m
	e.mu.Unlock()
	return e.State(), nil
}

// UpdateParam 校验后落库再生效。关掉实盘准入时，L2 以上自动降到 L1。
func (e *Engine) UpdateParam(ctx context.Context, key, value, operator string) (State, error) {
	if strings.TrimSpace(operator) == "" {
		return State{}, invalid("operator is required")
	}
	key = strings.TrimPrefix(strings.TrimSpace(key), risk.KeyPrefix)
	e.mu.RLock()
	next := e.params.Clone()
	e.mu.RUnlock()
	old, _ := next.Get(key)
	if err := next.Set(key, value); err != nil {
		return State{}, invalid("%v", err)
	}
	canonical, _ := next.Get(key)
	if err := e.repo.SaveParam(ctx, risk.KeyPrefix+key, canonical, old, operator); err != nil {
		return State{}, err
	}
	e.mu.Lock()
	cur := e.params.Clone()
	_ = cur.Set(key, canonical)
	e.params = cur
	downgrade := e.mode >= risk.L2 && !cur.LiveAdmitted
	if downgrade {
		e.mode = risk.L1
	}
	e.mu.Unlock()
	if downgrade {
		if err := e.repo.SaveMode(ctx, risk.L1, operator, "实盘准入关闭，自动降到 L1"); err != nil {
			e.log.Errorf("save mode: %v", err)
		}
	}
	return e.State(), nil
}

// State 是对外展示的状态。
type State struct {
	Mode          risk.Mode
	Kill          risk.KillState
	Breakers      map[string]string
	MarketBreaker string
	BuysToday     int
	Params        map[string]string
	AccountAsOf   map[string]time.Time
	LastQuoteAt   time.Time
}

func (e *Engine) State() State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s := State{
		Mode: e.mode, Kill: e.kill, MarketBreaker: e.marketBrk, Params: e.params.Values(),
		Breakers: map[string]string{}, AccountAsOf: map[string]time.Time{}, LastQuoteAt: e.lastQuoteAt,
	}
	for k, v := range e.breakers {
		s.Breakers[string(k)] = v
	}
	for k, a := range e.accounts {
		s.AccountAsOf[string(k)] = a.AsOf
	}
	for _, n := range e.buys {
		s.BuysToday += n
	}
	return s
}

// Report 返回某个上海日期的日度风险报告。
func (e *Engine) Report(ctx context.Context, date string) (string, *Report, error) {
	day := e.now().In(tradecal.Shanghai())
	if date != "" {
		d, err := time.ParseInLocation("2006-01-02", date, tradecal.Shanghai())
		if err != nil {
			return "", nil, invalid("date must be YYYY-MM-DD")
		}
		day = d
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, tradecal.Shanghai())
	r, err := e.repo.Report(ctx, from, from.AddDate(0, 0, 1))
	return from.Format("2006-01-02"), r, err
}
