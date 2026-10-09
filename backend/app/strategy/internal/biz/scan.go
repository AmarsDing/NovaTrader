package biz

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"server/pkg/events"
	"server/pkg/rules"

	"github.com/google/uuid"
)

const (
	PoolPre      = "pre"
	PoolIntraday = "intraday"

	StageRanked   = "ranked"
	StageAIScored = "ai_scored"
	StageSelected = "selected"
)

// 没入选的原因，写进 strategy_candidates.miss_reason。
const (
	MissHeld       = "held"
	MissCooldown   = "cooldown"
	MissNotInAITop = "not_in_ai_top"
	MissAIInvalid  = "ai_invalid"
	MissBelow      = "below_threshold"
	MissDailyCap   = "daily_cap"
	MissPrice      = "price_invalid"
	MissKillSwitch = "killswitch"
	MissCutoff     = "after_cutoff"
	MissNoQuote    = "no_quote"
)

// ScanRequest 描述一次扫描。Full 为真扫全市场；否则只扫 Symbols 加上当日盘前池。
type ScanRequest struct {
	Pool    string
	Full    bool
	Symbols []string
	AsOf    time.Time
}

// ScanReport 是一次扫描的统计，写日志和给客户端看。
type ScanReport struct {
	TraceID   string
	Pool      string
	Universe  int
	Dropped   map[string]int
	Matched   int
	Ranked    int
	AICalls   int
	Degraded  int
	Signals   []int
	Misses    map[string]int
	Elapsed   time.Duration
	KillState bool
}

type scored struct {
	cand  rules.Candidate
	row   *Candidate
	final float64
	ai    *AnalyzeOutput
	degr  bool
}

// Scan 跑一次漏斗。盘前池只记录候选；盘中才出买入信号。同一时刻只跑一个扫描，避免重复出信号。
func (uc *Usecase) Scan(ctx context.Context, req ScanRequest) (*ScanReport, error) {
	uc.scanMu.Lock()
	defer uc.scanMu.Unlock()
	start := time.Now()
	if req.AsOf.IsZero() {
		req.AsOf = time.Now()
	}
	if req.Pool == "" {
		req.Pool = PoolIntraday
	}
	trace := events.TraceID(ctx)
	if trace == "" {
		trace = uuid.NewString()
		ctx = events.WithTraceID(ctx, trace)
	}
	day := tradeDay(req.AsOf)
	p := uc.loadParams(ctx)
	rep := &ScanReport{TraceID: trace, Pool: req.Pool, Misses: map[string]int{}, KillState: uc.KillSwitch()}

	snaps, err := uc.snapshots(ctx, req, day)
	if err != nil {
		return nil, err
	}
	rep.Universe = len(snaps)
	black, err := uc.blacklist.Active(ctx, req.AsOf)
	if err != nil {
		return nil, fmt.Errorf("strategy: blacklist: %w", err)
	}
	res := rules.Screen(ctx, snaps, p.Strategies, p.Funnel, func(s string) bool { return black[s] })
	rep.Dropped, rep.Matched, rep.Ranked = res.Dropped, res.Matched, len(res.Ranked)

	rows := make([]*Candidate, len(res.Ranked))
	for i, c := range res.Ranked {
		rows[i] = &Candidate{
			TradeDate: day, Strategy: c.Strategy.Name(), VersionID: p.VersionID,
			Symbol: c.Snapshot.Symbol, Name: c.Snapshot.Name, Pool: req.Pool,
			Stage: StageRanked, RuleScore: c.Score, RefPrice: c.Snapshot.Last(),
		}
	}
	save := func() error {
		list := make([]Candidate, len(rows))
		for i, r := range rows {
			list[i] = *r
			if r.MissReason != "" {
				rep.Misses[r.MissReason]++
			}
		}
		if err := uc.candidates.Save(ctx, list); err != nil {
			return fmt.Errorf("strategy: save candidates: %w", err)
		}
		rep.Elapsed = time.Since(start)
		return nil
	}
	missAll := func(reason string) {
		for _, r := range rows {
			r.MissReason = reason
		}
	}

	switch {
	case req.Pool == PoolPre:
		return rep, save()
	case uc.KillSwitch():
		missAll(MissKillSwitch)
		return rep, save()
	case minuteOfDay(req.AsOf) >= uc.cfg.BuyCutoff:
		missAll(MissCutoff)
		return rep, save()
	}

	held, err := uc.heldSet(ctx)
	if err != nil {
		return nil, err
	}
	var eligible []int
	for i, c := range res.Ranked {
		sym := c.Snapshot.Symbol
		if c.Snapshot.Today == nil {
			rows[i].MissReason = MissNoQuote
			continue
		}
		if held[sym] {
			rows[i].MissReason = MissHeld
			continue
		}
		cooled, err := uc.cooled(ctx, sym, rules.SideBuy, req.AsOf, p)
		if err != nil {
			return nil, err
		}
		if cooled {
			rows[i].MissReason = MissCooldown
			continue
		}
		eligible = append(eligible, i)
	}
	if len(eligible) > p.AITopN {
		for _, i := range eligible[p.AITopN:] {
			rows[i].MissReason = MissNotInAITop
		}
		eligible = eligible[:p.AITopN]
	}

	outs := uc.analyzeAll(ctx, res.Ranked, eligible, trace, req.AsOf)
	rep.AICalls = len(eligible)
	var picks []scored
	for k, i := range eligible {
		c, row, out := res.Ranked[i], rows[i], outs[k]
		if out != nil && out.Discarded {
			row.MissReason = MissAIInvalid
			continue
		}
		var ai *float64
		if out != nil && !out.Degraded {
			v := out.Score
			ai = &v
		}
		final, degraded := p.Fuse.Fuse(c.Score, ai, c.Snapshot.Stage)
		if degraded {
			rep.Degraded++
		}
		row.Stage, row.AIScore, row.FinalScore = StageAIScored, ai, &final
		if ok, _ := p.Fuse.Selected(final); !ok {
			row.MissReason = MissBelow
			continue
		}
		picks = append(picks, scored{cand: c, row: row, final: final, ai: out, degr: degraded})
	}
	sort.SliceStable(picks, func(a, b int) bool { return picks[a].final > picks[b].final })

	used, err := uc.signals.CountBuys(ctx, uc.cfg.Book, day)
	if err != nil {
		return nil, err
	}
	left := p.MaxDaily - used
	for _, pk := range picks {
		if left <= 0 {
			pk.row.MissReason = MissDailyCap
			continue
		}
		sig, ok := uc.buildBuy(ctx, pk, p, req.AsOf, day, trace)
		if !ok {
			pk.row.MissReason = MissPrice
			continue
		}
		id, err := uc.signals.Create(ctx, sig, signalEnvelope)
		if err != nil {
			return nil, fmt.Errorf("strategy: create signal: %w", err)
		}
		pk.row.Stage, pk.row.MissReason, pk.row.SignalID, pk.row.RefPrice = StageSelected, "", &id, sig.Entry
		rep.Signals = append(rep.Signals, id)
		left--
	}
	return rep, save()
}

func (uc *Usecase) loadParams(ctx context.Context) Params {
	values, err := uc.params.Values(ctx)
	if err != nil {
		uc.log.Warnf("strategy_config unavailable, using defaults: %v", err)
		values = nil
	}
	version, err := uc.params.ActiveVersion(ctx)
	if err != nil {
		uc.log.Warnf("strategy_version unavailable: %v", err)
		version = nil
	}
	return LoadParams(values, version)
}

func (uc *Usecase) snapshots(ctx context.Context, req ScanRequest, day time.Time) ([]rules.Snapshot, error) {
	if req.Full || req.Pool == PoolPre {
		snaps, err := uc.market.Universe(ctx, req.AsOf)
		if err != nil {
			return nil, fmt.Errorf("strategy: universe: %w", err)
		}
		return snaps, nil
	}
	pre, err := uc.candidates.PoolSymbols(ctx, day, PoolPre)
	if err != nil {
		return nil, fmt.Errorf("strategy: pre pool: %w", err)
	}
	set := map[string]struct{}{}
	var symbols []string
	for _, s := range append(append([]string(nil), req.Symbols...), pre...) {
		if _, ok := set[s]; ok || s == "" {
			continue
		}
		set[s] = struct{}{}
		symbols = append(symbols, s)
	}
	if len(symbols) == 0 {
		return nil, nil
	}
	snaps, err := uc.market.Snapshots(ctx, symbols, req.AsOf)
	if err != nil {
		return nil, fmt.Errorf("strategy: snapshots: %w", err)
	}
	return snaps, nil
}

func (uc *Usecase) heldSet(ctx context.Context) (map[string]bool, error) {
	list, err := uc.positions.Holdings(ctx, uc.cfg.Book)
	if err != nil {
		return nil, fmt.Errorf("strategy: positions: %w", err)
	}
	out := make(map[string]bool, len(list))
	for _, h := range list {
		if h.Quantity > 0 {
			out[h.Symbol] = true
		}
	}
	return out, nil
}

// cooled 判断同股同方向是否在冷却：有未结信号，或买入在 BuyCooldown 内出过，或卖出终结不到 SellCooldown。
func (uc *Usecase) cooled(ctx context.Context, symbol, side string, now time.Time, p Params) (bool, error) {
	open, err := uc.signals.HasOpen(ctx, uc.cfg.Book, symbol, side)
	if err != nil || open {
		return open, err
	}
	last, err := uc.signals.Latest(ctx, uc.cfg.Book, symbol, side)
	if err != nil || last == nil {
		return false, err
	}
	if side == rules.SideBuy {
		return now.Sub(last.CreatedAt) < p.BuyCooldown, nil
	}
	return now.Sub(last.UpdatedAt) < p.SellCooldown, nil
}

// analyzeAll 并发调用 M05。返回与 idx 一一对应；nil 表示模型不可用。
func (uc *Usecase) analyzeAll(ctx context.Context, ranked []rules.Candidate, idx []int, trace string, asOf time.Time) []*AnalyzeOutput {
	outs := make([]*AnalyzeOutput, len(idx))
	if uc.analyzer == nil || len(idx) == 0 {
		return outs
	}
	n := uc.cfg.AIConcurrency
	if n <= 0 {
		n = 1
	}
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for k, i := range idx {
		wg.Add(1)
		sem <- struct{}{}
		go func(k int, c rules.Candidate) {
			defer wg.Done()
			defer func() { <-sem }()
			actx, cancel := context.WithTimeout(ctx, uc.cfg.AITimeout)
			defer cancel()
			out, err := uc.analyzer.Analyze(actx, analyzeInput(c, trace, asOf))
			if err != nil {
				uc.log.Warnf("brain analyze %s: %v", c.Snapshot.Symbol, err)
				return
			}
			outs[k] = &out
		}(k, ranked[i])
	}
	wg.Wait()
	return outs
}

func analyzeInput(c rules.Candidate, trace string, asOf time.Time) AnalyzeInput {
	s := c.Snapshot
	facts := []Fact{
		{ID: "F:m06_strategy", Label: "选股模板", Text: c.Strategy.Name(), Dim: "technical"},
		{ID: "F:m06_rule_score", Label: "规则分", Value: c.Score, Dim: "technical"},
		{ID: "F:m06_prev_close", Label: "昨收", Value: s.PrevClose(), Unit: "元", Dim: "technical"},
		{ID: "F:m06_last", Label: "现价", Value: s.Last(), Unit: "元", Dim: "technical"},
	}
	if s.Today != nil {
		facts = append(facts,
			Fact{ID: "F:m06_open", Label: "今开", Value: s.Today.Open, Unit: "元", Dim: "technical"},
			Fact{ID: "F:m06_amount", Label: "今日成交额", Value: s.Today.Amount, Unit: "元", Dim: "capital"},
		)
	}
	return AnalyzeInput{
		Symbol: s.Symbol, Name: s.Name, TraceID: trace, RuleScore: c.Score,
		Stage: s.Stage, AsOf: asOf, Facts: facts,
	}
}

func (uc *Usecase) buildBuy(ctx context.Context, pk scored, p Params, asOf, day time.Time, trace string) (*Signal, bool) {
	c := pk.cand
	plan, ok := c.Strategy.EntryPlan(ctx, c.Snapshot)
	if !ok {
		return nil, false
	}
	lv, err := p.Price.Buy(c.Snapshot, plan.Price)
	if err != nil {
		uc.log.Infof("skip %s: %v", c.Snapshot.Symbol, err)
		return nil, false
	}
	_, high := p.Fuse.Selected(pk.final)
	sig := &Signal{
		Book: uc.cfg.Book, Time: asOf, TradeDate: day, Symbol: c.Snapshot.Symbol, Name: c.Snapshot.Name,
		Side: rules.SideBuy, Strategy: c.Strategy.Name(), VersionID: p.VersionID,
		Entry: lv.Entry, EntryLow: lv.EntryLow, EntryHigh: lv.EntryHigh,
		StopLoss: lv.StopLoss, TakeProfit: lv.TakeProfit, ATR: lv.ATR,
		PositionPct: p.Price.PositionPct(high), ValidUntil: validUntil(asOf, p.TTL), HoldDaysMax: plan.HoldDays,
		RuleScore: c.Score, AIScore: pk.row.AIScore, FinalScore: pk.final,
		AIDegraded: pk.degr, HighValue: high, Reason: plan.Reason,
		Status: StatusPending, TraceID: trace,
	}
	if out := pk.ai; out != nil && !out.Degraded {
		sig.Dims, sig.Evidence = out.Dims, out.Evidence
		if out.Summary != "" {
			sig.Reason += "。研判：" + out.Summary
		}
	}
	if pk.degr {
		sig.Reason += "。模型不可用，只用规则分"
	}
	return sig, true
}
