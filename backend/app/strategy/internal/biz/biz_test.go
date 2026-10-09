package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"
	"time"

	"server/pkg/events"
	"server/pkg/rules"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

var (
	ctx   = context.Background()
	asOf  = time.Date(2026, 10, 9, 10, 0, 0, 0, tradecal.Shanghai())
	since = asOf.AddDate(-2, 0, 0)
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %.4f, want %.4f", name, got, want)
	}
}

// firstBoard 与 pkg/rules 测试的首板弱转强样本相同：规则分约 86.7，高价值。
func firstBoard(symbol string) rules.Snapshot {
	bars := make([]rules.Bar, 29)
	for i := range bars {
		bars[i] = rules.Bar{Open: 10, High: 10.1, Low: 9.9, Close: 10, Volume: 1e7, Amount: 1e8}
	}
	bars = append(bars, rules.Bar{Open: 10.2, High: 11, Low: 10.1, Close: 11, Volume: 2.3e7, Amount: 2.5e8})
	return rules.Snapshot{
		Symbol: symbol, Name: "样本" + symbol, ListDate: since, AsOf: asOf, Stage: rules.StageWarm, Bars: bars,
		Today: &rules.Bar{Open: 11.22, High: 11.5, Low: 11.2, Close: 11.45, Volume: 5e6, Amount: 5.7e7},
	}
}

type fakeMarket struct {
	snaps  []rules.Snapshot
	closes map[string]map[string]float64
}

func (m *fakeMarket) Universe(_ context.Context, asOf time.Time) ([]rules.Snapshot, error) {
	out := make([]rules.Snapshot, len(m.snaps))
	for i, s := range m.snaps {
		s.AsOf = asOf
		out[i] = s
	}
	return out, nil
}

func (m *fakeMarket) Snapshots(ctx context.Context, symbols []string, asOf time.Time) ([]rules.Snapshot, error) {
	want := map[string]bool{}
	for _, s := range symbols {
		want[s] = true
	}
	all, _ := m.Universe(ctx, asOf)
	var out []rules.Snapshot
	for _, s := range all {
		if want[s.Symbol] {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *fakeMarket) DailyCloses(_ context.Context, symbol string, _, _ time.Time) (map[string]float64, error) {
	return m.closes[symbol], nil
}

type fakeAnalyzer struct {
	mu    sync.Mutex
	calls int
	fn    func(AnalyzeInput) (AnalyzeOutput, error)
}

func (a *fakeAnalyzer) Analyze(_ context.Context, in AnalyzeInput) (AnalyzeOutput, error) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	return a.fn(in)
}

type fakeParams struct{ values map[string]string }

func (p fakeParams) Values(context.Context) (map[string]string, error) { return p.values, nil }
func (p fakeParams) ActiveVersion(context.Context) (*int, error) {
	v := 7
	return &v, nil
}

type fakeSignals struct {
	mu     sync.Mutex
	rows   map[int]*Signal
	next   int
	events []events.Envelope
	trail  []string
}

func newFakeSignals() *fakeSignals { return &fakeSignals{rows: map[int]*Signal{}} }

func (f *fakeSignals) Create(_ context.Context, s *Signal, env EnvelopeFunc) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	cp := *s
	cp.ID = f.next
	cp.CreatedAt, cp.UpdatedAt = s.Time, s.Time
	e, err := env(&cp, "")
	if err != nil {
		return 0, err
	}
	f.rows[cp.ID] = &cp
	f.events = append(f.events, e)
	f.trail = append(f.trail, fmt.Sprintf("%d:->%s", cp.ID, cp.Status))
	s.ID = cp.ID
	return cp.ID, nil
}

func (f *fakeSignals) Transition(_ context.Context, id int, from, to Status, _, actor, _ string, env EnvelopeFunc, patch *SignalPatch) (*Signal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok || row.Status != from {
		return nil, ErrConflict
	}
	row.Status = to
	if patch != nil {
		row.ExitPrice, row.PnlPct, row.HoldingDays = patch.ExitPrice, patch.PnlPct, patch.HoldingDays
		if patch.ClosedAt != nil {
			row.ClosedAt = *patch.ClosedAt
		}
	}
	row.UpdatedAt = row.UpdatedAt.Add(time.Second)
	e, err := env(row, from)
	if err != nil {
		return nil, err
	}
	f.events = append(f.events, e)
	f.trail = append(f.trail, fmt.Sprintf("%d:%s->%s by %s", id, from, to, actor))
	cp := *row
	return &cp, nil
}

func (f *fakeSignals) Get(_ context.Context, id int) (*Signal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *row
	return &cp, nil
}

func (f *fakeSignals) sorted(match func(*Signal) bool) []*Signal {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Signal
	for _, s := range f.rows {
		if match(s) {
			cp := *s
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID > out[b].ID })
	return out
}

func (f *fakeSignals) Latest(_ context.Context, book, symbol, side string) (*Signal, error) {
	l := f.sorted(func(s *Signal) bool { return s.Book == book && s.Symbol == symbol && s.Side == side })
	if len(l) == 0 {
		return nil, nil
	}
	return l[0], nil
}

func (f *fakeSignals) HasOpen(_ context.Context, book, symbol, side string) (bool, error) {
	l := f.sorted(func(s *Signal) bool {
		open := s.Status == StatusPending || s.Status == StatusRiskChecking || s.Status == StatusApproved || s.Status == StatusExecuting
		return s.Book == book && s.Symbol == symbol && s.Side == side && open
	})
	return len(l) > 0, nil
}

func (f *fakeSignals) CountBuys(_ context.Context, book string, day time.Time) (int, error) {
	return len(f.sorted(func(s *Signal) bool {
		return s.Book == book && s.Side == rules.SideBuy && s.TradeDate.Equal(day)
	})), nil
}

func (f *fakeSignals) Due(_ context.Context, now time.Time) ([]*Signal, error) {
	return f.sorted(func(s *Signal) bool {
		return (s.Status == StatusPending || s.Status == StatusApproved) && s.ValidUntil.Before(now)
	}), nil
}

func (f *fakeSignals) LastDoneBuy(_ context.Context, book, symbol string) (*Signal, error) {
	l := f.sorted(func(s *Signal) bool {
		return s.Book == book && s.Symbol == symbol && s.Side == rules.SideBuy && s.Status == StatusDone
	})
	if len(l) == 0 {
		return nil, nil
	}
	return l[0], nil
}

func (f *fakeSignals) List(_ context.Context, flt SignalFilter) ([]*Signal, error) {
	l := f.sorted(func(s *Signal) bool { return flt.Status == "" || s.Status == flt.Status })
	if flt.Limit > 0 && len(l) > flt.Limit {
		l = l[:flt.Limit]
	}
	return l, nil
}

func (f *fakeSignals) put(s Signal) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	s.ID = f.next
	f.rows[s.ID] = &s
	return s.ID
}

type fakeCandidates struct {
	mu   sync.Mutex
	rows map[string]*Candidate
	next int
}

func newFakeCandidates() *fakeCandidates { return &fakeCandidates{rows: map[string]*Candidate{}} }

func candKey(c Candidate) string {
	return c.TradeDate.Format("2006-01-02") + "|" + c.Strategy + "|" + c.Symbol
}

func (f *fakeCandidates) Save(_ context.Context, list []Candidate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range list {
		k := candKey(c)
		old, ok := f.rows[k]
		if ok && old.Stage == StageSelected {
			continue
		}
		cp := c
		if ok {
			cp.ID, cp.Pool = old.ID, old.Pool
		} else {
			f.next++
			cp.ID = f.next
		}
		f.rows[k] = &cp
	}
	return nil
}

func (f *fakeCandidates) PoolSymbols(_ context.Context, day time.Time, pool string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.rows {
		if c.TradeDate.Equal(day) && c.Pool == pool {
			out = append(out, c.Symbol)
		}
	}
	return out, nil
}

func (f *fakeCandidates) PendingOutcomes(_ context.Context, since, today time.Time) ([]Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Candidate
	for _, c := range f.rows {
		if !c.TradeDate.Before(since) && c.TradeDate.Before(today) && c.RetT5 == nil {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (f *fakeCandidates) SetOutcome(_ context.Context, id int, t1, t3, t5 *float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.rows {
		if c.ID == id {
			c.RetT1, c.RetT3, c.RetT5 = t1, t3, t5
			return nil
		}
	}
	return ErrNotFound
}

func (f *fakeCandidates) List(context.Context, CandidateFilter) ([]Candidate, error) { return nil, nil }

func (f *fakeCandidates) get(symbol string) *Candidate {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.rows {
		if c.Symbol == symbol {
			cp := *c
			return &cp
		}
	}
	return nil
}

type fakeBlacklist struct{ codes map[string]bool }

func (b *fakeBlacklist) Active(context.Context, time.Time) (map[string]bool, error) {
	return b.codes, nil
}
func (b *fakeBlacklist) Add(_ context.Context, symbol, _, _ string, _ *time.Time) error {
	b.codes[symbol] = true
	return nil
}
func (b *fakeBlacklist) Remove(_ context.Context, symbol string) error {
	delete(b.codes, symbol)
	return nil
}

type fakePositions struct{ list []Holding }

func (p *fakePositions) Holdings(context.Context, string) ([]Holding, error) { return p.list, nil }

type env struct {
	uc      *Usecase
	market  *fakeMarket
	ai      *fakeAnalyzer
	signals *fakeSignals
	cands   *fakeCandidates
	black   *fakeBlacklist
	pos     *fakePositions
}

func newEnv(t *testing.T, ai *fakeAnalyzer, values map[string]string, snaps ...rules.Snapshot) *env {
	t.Helper()
	e := &env{
		market: &fakeMarket{snaps: snaps, closes: map[string]map[string]float64{}}, ai: ai,
		signals: newFakeSignals(), cands: newFakeCandidates(),
		black: &fakeBlacklist{codes: map[string]bool{}}, pos: &fakePositions{},
	}
	var analyzer Analyzer
	if ai != nil {
		analyzer = ai
	}
	e.uc = NewUsecase(NewSettings(nil), e.market, analyzer, fakeParams{values: values}, e.signals,
		e.cands, e.black, e.pos, log.DefaultLogger)
	return e
}

func scoreAI(score float64) *fakeAnalyzer {
	return &fakeAnalyzer{fn: func(in AnalyzeInput) (AnalyzeOutput, error) {
		return AnalyzeOutput{
			DecisionID: 1, Score: score, Summary: "资金承接好",
			Dims: map[string]float64{"technical": 80, "news": 60}, Evidence: []string{"F:m06_rule_score"},
		}, nil
	}}
}

func scan(t *testing.T, e *env, req ScanRequest) *ScanReport {
	t.Helper()
	if req.AsOf.IsZero() {
		req.AsOf = asOf
	}
	if req.Pool == "" {
		req.Pool = PoolIntraday
		req.Full = true
	}
	rep, err := e.uc.Scan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestScanCreatesSignal(t *testing.T) {
	e := newEnv(t, scoreAI(80), nil, firstBoard("600000.SH"))
	rep := scan(t, e, ScanRequest{})
	if len(rep.Signals) != 1 || rep.AICalls != 1 || rep.Degraded != 0 {
		t.Fatalf("report %+v", rep)
	}
	sig, _ := e.signals.Get(ctx, rep.Signals[0])
	rule := sig.RuleScore
	if rule < 85 || rule > 88 {
		t.Fatalf("rule score %.2f", rule)
	}
	near(t, "final", sig.FinalScore, 0.4*rule+0.6*80, 1e-9)
	if sig.AIScore == nil || *sig.AIScore != 80 || sig.AIDegraded || sig.HighValue {
		t.Fatalf("ai fields %+v", sig)
	}
	near(t, "position", sig.PositionPct, 0.03, 1e-9)
	if sig.Strategy != rules.NameFirstBoard || sig.Side != rules.SideBuy || sig.Status != StatusPending {
		t.Fatalf("signal %+v", sig)
	}
	if !(sig.StopLoss < sig.EntryLow && sig.EntryLow <= sig.Entry && sig.Entry <= sig.EntryHigh && sig.EntryHigh < sig.TakeProfit) {
		t.Fatalf("levels %+v", sig)
	}
	if !sig.ValidUntil.Equal(asOf.Add(30*time.Minute)) || sig.HoldDaysMax != 2 || sig.TraceID == "" || sig.ATR <= 0 {
		t.Fatalf("meta %+v", sig)
	}
	if sig.VersionID == nil || *sig.VersionID != 7 || sig.Dims["technical"] != 80 || len(sig.Evidence) != 1 {
		t.Fatalf("version/dims %+v", sig)
	}
	c := e.cands.get("600000.SH")
	if c == nil || c.Stage != StageSelected || c.SignalID == nil || *c.SignalID != sig.ID || c.RefPrice != sig.Entry {
		t.Fatalf("candidate %+v", c)
	}
	if len(e.signals.events) != 1 || e.signals.events[0].Subject != events.SubjectSignal {
		t.Fatalf("events %+v", e.signals.events)
	}
	var p map[string]any
	_ = json.Unmarshal(e.signals.events[0].Payload, &p)
	if p["stock_code"] != "600000.SH" || p["signal_type"] != "buy" || p["status"] != "pending" || p["signal_time"] == nil {
		t.Fatalf("payload %v", p)
	}
}

func TestScanDegraded(t *testing.T) {
	for name, ai := range map[string]*fakeAnalyzer{
		"no analyzer": nil,
		"error":       {fn: func(AnalyzeInput) (AnalyzeOutput, error) { return AnalyzeOutput{}, errors.New("down") }},
		"degraded":    {fn: func(AnalyzeInput) (AnalyzeOutput, error) { return AnalyzeOutput{Degraded: true, Score: 99}, nil }},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, ai, nil, firstBoard("600000.SH"))
			rep := scan(t, e, ScanRequest{})
			if len(rep.Signals) != 1 || rep.Degraded != 1 {
				t.Fatalf("report %+v", rep)
			}
			sig, _ := e.signals.Get(ctx, rep.Signals[0])
			if !sig.AIDegraded || sig.AIScore != nil || !sig.HighValue {
				t.Fatalf("signal %+v", sig)
			}
			near(t, "final", sig.FinalScore, sig.RuleScore, 1e-9)
			near(t, "position", sig.PositionPct, 0.05, 1e-9)
		})
	}
}

func TestScanDiscardedAndBelow(t *testing.T) {
	e := newEnv(t, &fakeAnalyzer{fn: func(AnalyzeInput) (AnalyzeOutput, error) {
		return AnalyzeOutput{Discarded: true, Reason: "证据不足"}, nil
	}}, nil, firstBoard("600000.SH"))
	if rep := scan(t, e, ScanRequest{}); len(rep.Signals) != 0 || rep.Misses[MissAIInvalid] != 1 {
		t.Fatalf("discarded %+v", rep)
	}
	e = newEnv(t, scoreAI(20), nil, firstBoard("600000.SH"))
	if rep := scan(t, e, ScanRequest{}); len(rep.Signals) != 0 || rep.Misses[MissBelow] != 1 {
		t.Fatalf("below %+v", rep)
	}
	if c := e.cands.get("600000.SH"); c.Stage != StageAIScored || c.AIScore == nil || c.FinalScore == nil {
		t.Fatalf("candidate %+v", c)
	}
}

func TestScanGuards(t *testing.T) {
	t.Run("pre pool only records", func(t *testing.T) {
		e := newEnv(t, scoreAI(90), nil, firstBoard("600000.SH"))
		rep := scan(t, e, ScanRequest{Pool: PoolPre, AsOf: asOf.Add(-time.Hour)})
		if len(rep.Signals) != 0 || e.ai.calls != 0 {
			t.Fatalf("pre %+v calls=%d", rep, e.ai.calls)
		}
		if c := e.cands.get("600000.SH"); c == nil || c.Pool != PoolPre {
			t.Fatalf("candidate %+v", c)
		}
		// 盘中非全市场扫描会带上盘前池。
		rep = scan(t, e, ScanRequest{Pool: PoolIntraday})
		if rep.Universe != 1 || len(rep.Signals) != 1 {
			t.Fatalf("intraday should include pre pool: %+v", rep)
		}
	})
	t.Run("no live quote", func(t *testing.T) {
		s := firstBoard("600000.SH")
		s.Today = nil
		e := newEnv(t, scoreAI(90), nil, s)
		rep := scan(t, e, ScanRequest{})
		if len(rep.Signals) != 0 || rep.Misses[MissNoQuote] != 1 || e.ai.calls != 0 {
			t.Fatalf("no quote %+v calls=%d", rep, e.ai.calls)
		}
	})
	t.Run("kill switch", func(t *testing.T) {
		e := newEnv(t, scoreAI(90), nil, firstBoard("600000.SH"))
		e.uc.SetKillSwitch(true)
		rep := scan(t, e, ScanRequest{})
		if len(rep.Signals) != 0 || rep.Misses[MissKillSwitch] != 1 || !rep.KillState || e.ai.calls != 0 {
			t.Fatalf("kill %+v", rep)
		}
	})
	t.Run("cutoff", func(t *testing.T) {
		e := newEnv(t, scoreAI(90), nil, firstBoard("600000.SH"))
		rep := scan(t, e, ScanRequest{AsOf: time.Date(2026, 10, 9, 14, 50, 0, 0, tradecal.Shanghai())})
		if len(rep.Signals) != 0 || rep.Misses[MissCutoff] != 1 {
			t.Fatalf("cutoff %+v", rep)
		}
	})
	t.Run("held", func(t *testing.T) {
		e := newEnv(t, scoreAI(90), nil, firstBoard("600000.SH"))
		e.pos.list = []Holding{{Symbol: "600000.SH", Quantity: 100}}
		if rep := scan(t, e, ScanRequest{}); len(rep.Signals) != 0 || rep.Misses[MissHeld] != 1 {
			t.Fatalf("held %+v", rep)
		}
	})
	t.Run("blacklist", func(t *testing.T) {
		e := newEnv(t, scoreAI(90), nil, firstBoard("600000.SH"))
		code, err := e.uc.AddBlacklist(ctx, "600000.sh", "立案调查", "user", nil)
		if err != nil || code != "600000.SH" {
			t.Fatalf("code=%s err=%v", code, err)
		}
		if rep := scan(t, e, ScanRequest{}); len(rep.Signals) != 0 || rep.Dropped[rules.DropBlacklist] != 1 {
			t.Fatalf("blacklist %+v", rep)
		}
		if _, err := e.uc.AddBlacklist(ctx, "600000.SH", " ", "user", nil); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("empty reason err=%v", err)
		}
	})
	t.Run("cooldown", func(t *testing.T) {
		e := newEnv(t, scoreAI(90), nil, firstBoard("600000.SH"))
		first := scan(t, e, ScanRequest{})
		if len(first.Signals) != 1 {
			t.Fatalf("first %+v", first)
		}
		// 未结信号挡住第二次。
		if rep := scan(t, e, ScanRequest{AsOf: asOf.Add(time.Minute)}); rep.Misses[MissCooldown] != 1 {
			t.Fatalf("open %+v", rep)
		}
		// 过期后仍在 240 分钟买入冷却内。
		if _, err := e.uc.ExpireDue(ctx, asOf.Add(31*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if rep := scan(t, e, ScanRequest{AsOf: asOf.Add(40 * time.Minute)}); rep.Misses[MissCooldown] != 1 {
			t.Fatalf("cooldown %+v", rep)
		}
	})
}

func TestDailyCap(t *testing.T) {
	e := newEnv(t, scoreAI(90), map[string]string{"m06.max_daily_signals": "1"},
		firstBoard("600000.SH"), firstBoard("600004.SH"))
	rep := scan(t, e, ScanRequest{})
	if len(rep.Signals) != 1 || rep.Misses[MissDailyCap] != 1 {
		t.Fatalf("cap %+v", rep)
	}
	if rep := scan(t, e, ScanRequest{AsOf: asOf.Add(time.Minute)}); len(rep.Signals) != 0 {
		t.Fatalf("cap reached %+v", rep)
	}
}

func TestTransition(t *testing.T) {
	e := newEnv(t, nil, nil)
	id := e.signals.put(Signal{Book: "paper", Symbol: "600000.SH", Side: rules.SideBuy, Status: StatusPending})
	bad := []struct {
		to    Status
		actor string
	}{
		{StatusApproved, ActorRisk},     // 跳过风控中
		{StatusRiskChecking, ActorUser}, // 只有风控能接
		{StatusDone, ActorTrade},
		{StatusExpired, ActorUser},
	}
	for _, c := range bad {
		if _, err := e.uc.Transition(ctx, id, c.to, "", c.actor, nil); !errors.Is(err, ErrBadTransition) {
			t.Fatalf("%s by %s err=%v", c.to, c.actor, err)
		}
	}
	steps := []struct {
		to    Status
		actor string
	}{{StatusRiskChecking, ActorRisk}, {StatusApproved, ActorRisk}, {StatusExecuting, ActorTrade}, {StatusDone, ActorTrade}}
	for _, s := range steps {
		if _, err := e.uc.Transition(ctx, id, s.to, "ok", s.actor, nil); err != nil {
			t.Fatalf("%s: %v", s.to, err)
		}
	}
	if _, err := e.uc.Transition(ctx, id, StatusCancelled, "", ActorUser, nil); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("terminal err=%v", err)
	}
	if _, err := e.uc.Transition(ctx, 999, StatusCancelled, "", ActorUser, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing err=%v", err)
	}
	if len(e.signals.events) != len(steps) {
		t.Fatalf("events %d", len(e.signals.events))
	}
	var p SignalUpdated
	_ = json.Unmarshal(e.signals.events[3].Payload, &p)
	if p.From != StatusExecuting || p.Status != StatusDone {
		t.Fatalf("payload %+v", p)
	}

	u := newEnv(t, nil, nil)
	x := u.signals.put(Signal{Status: StatusApproved})
	if _, err := u.uc.Transition(ctx, x, StatusCancelled, "人工撤销", ActorUser, nil); err != nil {
		t.Fatal(err)
	}
	y := u.signals.put(Signal{Status: StatusExecuting})
	if _, err := u.uc.Transition(ctx, y, StatusCancelled, "", ActorUser, nil); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("user cancel executing err=%v", err)
	}
}

func TestCloseRoundTrip(t *testing.T) {
	e := newEnv(t, nil, nil)
	bought := tradeDay(asOf.AddDate(0, 0, -1))
	id := e.signals.put(Signal{
		Book: "paper", Symbol: "600000.SH", Name: "浦发银行", Side: rules.SideBuy, Status: StatusDone,
		Strategy: rules.NameFirstBoard, Entry: 11.45, Time: bought.Add(10 * time.Hour), TradeDate: bought, TraceID: "t-close",
	})
	sell := e.signals.put(Signal{Side: rules.SideSell, Status: StatusDone, TradeDate: bought})
	at := asOf
	if _, err := e.uc.Transition(ctx, id, StatusClosed, "", ActorUser, &Close{ExitPrice: 12, PnlPct: 4.8, At: at}); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("user close err=%v", err)
	}
	if _, err := e.uc.Transition(ctx, id, StatusClosed, "", ActorTrade, nil); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("missing exit err=%v", err)
	}
	if _, err := e.uc.Transition(ctx, sell, StatusClosed, "", ActorTrade, &Close{ExitPrice: 12, PnlPct: 1, At: at}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("sell close err=%v", err)
	}
	sig, err := e.uc.Transition(ctx, id, StatusClosed, "仓位归零", ActorTrade, &Close{ExitPrice: 12, PnlPct: 4.8, At: at})
	if err != nil {
		t.Fatal(err)
	}
	if sig.Status != StatusClosed || sig.ExitPrice == nil || *sig.ExitPrice != 12 || sig.PnlPct == nil || *sig.PnlPct != 4.8 {
		t.Fatalf("closed %+v", sig)
	}
	if sig.HoldingDays == nil || *sig.HoldingDays != 1 || !sig.ClosedAt.Equal(at) {
		t.Fatalf("days=%v at=%s", sig.HoldingDays, sig.ClosedAt)
	}
	var p SignalUpdated
	_ = json.Unmarshal(e.signals.events[0].Payload, &p)
	if p.Status != StatusClosed || p.ExitPrice == nil || *p.PnlPct != 4.8 || p.ClosedAt == nil || p.StockCode != "600000.SH" {
		t.Fatalf("payload %+v", p)
	}
	if _, err := e.uc.Transition(ctx, id, StatusCancelled, "", ActorTrade, nil); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("after close err=%v", err)
	}
}

func TestExpireDue(t *testing.T) {
	e := newEnv(t, nil, nil)
	due := e.signals.put(Signal{Status: StatusPending, ValidUntil: asOf})
	approved := e.signals.put(Signal{Status: StatusApproved, ValidUntil: asOf})
	checking := e.signals.put(Signal{Status: StatusRiskChecking, ValidUntil: asOf})
	later := e.signals.put(Signal{Status: StatusPending, ValidUntil: asOf.Add(time.Hour)})
	n, err := e.uc.ExpireDue(ctx, asOf.Add(time.Second))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for id, want := range map[int]Status{due: StatusExpired, approved: StatusExpired, checking: StatusRiskChecking, later: StatusPending} {
		if s, _ := e.signals.Get(ctx, id); s.Status != want {
			t.Fatalf("%d status %s want %s", id, s.Status, want)
		}
	}
}

func TestScanExits(t *testing.T) {
	e := newEnv(t, nil, nil, firstBoard("600000.SH"))
	e.uc.SetKillSwitch(true)
	e.pos.list = []Holding{{Symbol: "600000.SH", Quantity: 1000, Available: 1000, AvgCost: 12}}
	e.signals.put(Signal{
		Book: "paper", Symbol: "600000.SH", Side: rules.SideBuy, Status: StatusDone, Strategy: rules.NameFirstBoard,
		StopLoss: 11.6, TakeProfit: 13, TradeDate: tradeDay(asOf.AddDate(0, 0, -1)),
	})
	ids, err := e.uc.ScanExits(ctx, asOf)
	if err != nil || len(ids) != 1 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	s, _ := e.signals.Get(ctx, ids[0])
	if s.Side != rules.SideSell || s.ExitKind != rules.ExitStopLoss || s.PositionPct != 1 || s.Strategy != rules.NameFirstBoard {
		t.Fatalf("sell %+v", s)
	}
	near(t, "sell price", s.Entry, 11.39, 0.011)
	// 有未结卖单时不重复出。
	if ids, _ := e.uc.ScanExits(ctx, asOf.Add(time.Minute)); len(ids) != 0 {
		t.Fatalf("dup %v", ids)
	}
	// 没有可卖数量不出。
	e2 := newEnv(t, nil, nil, firstBoard("600000.SH"))
	e2.pos.list = []Holding{{Symbol: "600000.SH", Quantity: 1000, Available: 0}}
	if ids, _ := e2.uc.ScanExits(ctx, asOf); len(ids) != 0 {
		t.Fatalf("T+1 locked %v", ids)
	}
}

func TestFillOutcomes(t *testing.T) {
	e := newEnv(t, nil, nil)
	day := time.Date(2026, 9, 21, 0, 0, 0, 0, tradecal.Shanghai())
	days, err := e.uc.nextOpenDays(day, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.cands.Save(ctx, []Candidate{{TradeDate: day, Strategy: "s", Symbol: "600000.SH", RefPrice: 10}}); err != nil {
		t.Fatal(err)
	}
	closes := map[string]float64{days[0].Format("2006-01-02"): 10.5, days[2].Format("2006-01-02"): 9}
	e.market.closes["600000.SH"] = closes

	// T+3 当天收盘后：T+5 还没到。
	n, err := e.uc.FillOutcomes(ctx, days[2].Add(16*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	c := e.cands.get("600000.SH")
	if c.RetT1 == nil || c.RetT3 == nil || c.RetT5 != nil {
		t.Fatalf("partial %+v", c)
	}
	near(t, "t1", *c.RetT1, 0.05, 1e-9)
	near(t, "t3", *c.RetT3, -0.1, 1e-9)

	closes[days[4].Format("2006-01-02")] = 11
	if n, err := e.uc.FillOutcomes(ctx, days[4].Add(16*time.Hour)); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	c = e.cands.get("600000.SH")
	near(t, "t5", *c.RetT5, 0.1, 1e-9)
	if n, _ := e.uc.FillOutcomes(ctx, days[4].Add(17*time.Hour)); n != 0 {
		t.Fatalf("refilled %d", n)
	}
}

func TestValidUntil(t *testing.T) {
	day := tradeDay(asOf)
	cases := map[time.Time]time.Time{
		day.Add(9 * time.Hour):                 day.Add(10 * time.Hour),
		day.Add(10 * time.Hour):                day.Add(10*time.Hour + 30*time.Minute),
		day.Add(14*time.Hour + 40*time.Minute): day.Add(14*time.Hour + 57*time.Minute),
	}
	for in, want := range cases {
		if got := validUntil(in, 30*time.Minute); !got.Equal(want) {
			t.Fatalf("validUntil(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestSettings(t *testing.T) {
	s := NewSettings(nil)
	if s.Book != "paper" || s.BuyCutoff != 14*60+50 || s.AIConcurrency != 4 {
		t.Fatalf("defaults %+v", s)
	}
	if _, ok := parseClock("25:00"); ok {
		t.Fatal("bad clock accepted")
	}
	p := LoadParams(map[string]string{"m06.ai_top_n": "10", "m06.signal_ttl_minutes": "x"}, nil)
	if p.AITopN != 10 || p.TTL != 30*time.Minute || len(p.Strategies) != 2 {
		t.Fatalf("params %+v", p)
	}
}
