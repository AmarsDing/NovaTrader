package biz

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"server/pkg/market"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

// autoStore 是 M13-T02 自动消费测试用的内存计划仓储：账本与意图真皮层
// 走 MemStore（含 LedgerStore），这里补齐 AutomationStore。PlanFacts 的账户
// 快照来自最近一次 CommitSnapshot——与 data 层读 AccountSnapshot 最新行的口径一致。
type autoStore struct {
	*MemStore

	mu       sync.Mutex
	facts    PlanFacts
	factsErr error
	plans    map[string]PlanRecord
}

func autoPlanKey(id string, rev int) string { return fmt.Sprintf("%s#%d", id, rev) }

func (a *autoStore) PlanFacts(_ context.Context, id int, book string) (PlanFacts, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.factsErr != nil {
		return PlanFacts{}, a.factsErr
	}
	a.MemStore.mu.Lock()
	defer a.MemStore.mu.Unlock()
	if len(a.states) == 0 {
		return PlanFacts{}, fmt.Errorf("账户快照未初始化")
	}
	p := a.facts
	p.SignalID = id
	p.Book = book
	p.Account = a.states[len(a.states)-1]
	return p, nil
}

func (a *autoStore) SavePlan(_ context.Context, in PlanRecord, base int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if base > 0 {
		key, found := "", false
		for k, r := range a.plans {
			if r.Plan.PlanID == in.Plan.PlanID && r.Plan.Revision == base && r.State != "executed" && r.State != "superseded" {
				key, found = k, true
				break
			}
		}
		if !found {
			return ErrConflict
		}
		r := a.plans[key]
		r.State = "superseded"
		a.plans[key] = r
	}
	key := autoPlanKey(in.Plan.PlanID, in.Plan.Revision)
	if _, dup := a.plans[key]; dup {
		return ErrConflict
	}
	a.plans[key] = in
	return nil
}

func (a *autoStore) GetPlan(_ context.Context, id string, revision int) (PlanRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if revision > 0 {
		if r, ok := a.plans[autoPlanKey(id, revision)]; ok {
			return r, nil
		}
		return PlanRecord{}, ErrNotFound
	}
	best := -1
	var out PlanRecord
	for k, r := range a.plans {
		_ = k
		if r.Plan.PlanID == id && r.Plan.Revision > best {
			best = r.Plan.Revision
			out = r
		}
	}
	if best < 0 {
		return PlanRecord{}, ErrNotFound
	}
	return out, nil
}

func (a *autoStore) ListPlans(_ context.Context, book, state string) ([]PlanRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []PlanRecord
	for _, r := range a.plans {
		if r.Plan.AccountType != accountOf(book) {
			continue
		}
		if state != "" && r.State != state {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (a *autoStore) SetPlanState(_ context.Context, id string, rev int, from, to, mode, operator, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := autoPlanKey(id, rev)
	r, ok := a.plans[key]
	if !ok || r.State != from {
		return ErrConflict
	}
	r.State, r.Mode, r.Operator, r.Reason = to, mode, operator, reason
	a.plans[key] = r
	return nil
}

func (a *autoStore) ListIntents(_ context.Context, book string) ([]Intent, error) {
	a.MemStore.mu.Lock()
	defer a.MemStore.mu.Unlock()
	var out []Intent
	for _, in := range a.MemStore.intents {
		if in.Book == book {
			out = append(out, in)
		}
	}
	return out, nil
}

func (a *autoStore) ListUnresolvedIntents(_ context.Context, book string) ([]Intent, error) {
	a.MemStore.mu.Lock()
	defer a.MemStore.mu.Unlock()
	var out []Intent
	for _, in := range a.MemStore.intents {
		if in.Book == book && (in.State == "claimed" || in.State == "unknown") {
			out = append(out, in)
		}
	}
	return out, nil
}

func newAutoEngine(t *testing.T) (*Engine, *autoStore) {
	t.Helper()
	a := &autoStore{MemStore: NewMemStore(), plans: map[string]PlanRecord{}}
	now := time.Now()
	a.facts = PlanFacts{
		Symbol: "600519.SH", Side: "buy", Strategy: "first_board_weak2strong",
		Status: "pending", Reason: "明确标记的合成规则fixture",
		StrategyRevision: 1, ConfigRevision: 1,
		Entry: 10, EntryLow: 9.8, EntryHigh: 10.2, StopLoss: 9, TakeProfit: 11, PositionPct: 0.05,
		RuleScore: 80, FormedAt: now, ValidUntil: now.Add(10 * time.Minute),
		Evidence: []string{"F:fixture"}, SignalHash: "fixture-hash",
		Quote: market.Snapshot{Symbol: "600519.SH", Time: now, AsOf: now, Last: 10, Source: "test"},
	}
	if err := a.MemStore.Commit(context.Background(), Batch{Cash: &Cash{Book: BookPaper, Cash: 1_000_000, DayStartDate: now.In(tradecal.Shanghai()).Format("2006-01-02"), DayStartEquity: 1_000_000}}); err != nil {
		t.Fatal(err)
	}
	return NewEngine(a, allow{}, Settings{InitialCash: 1_000_000, PaperDaysRequired: 20}, log.DefaultLogger), a
}

func autoSignal(id int) AutoSignal {
	return AutoSignal{
		SignalID: id, Book: "paper", Status: "pending", Symbol: "600519.SH",
		Strategy: "first_board_weak2strong", SignalType: "buy", FinalScore: 85,
	}
}

func TestAutoConsumeFilters(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(*AutoSignal)
		gates    func(*AutoGates)
		wantSkip string
	}{
		{"non-buy direction ignored", func(s *AutoSignal) { s.SignalType = "sell" }, nil, "non_buy"},
		{"non-pending ignored", func(s *AutoSignal) { s.Status = "approved" }, nil, "status"},
		{"below threshold ignored", func(s *AutoSignal) { s.FinalScore = 79.5 }, nil, "score"},
		{"threshold boundary passes", func(s *AutoSignal) { s.FinalScore = 80 }, nil, ""},
		{"template outside whitelist ignored", nil, func(g *AutoGates) {
			g.Templates = map[string]bool{"pullback_dip": true}
		}, "template"},
		{"degraded governance default-off", func(s *AutoSignal) { s.AIDegraded = true }, nil, "ai_degraded"},
		{"degraded allowed passes", func(s *AutoSignal) { s.AIDegraded = true }, func(g *AutoGates) {
			g.AllowDegraded = true
		}, ""},
		{"happy path authorizes sim_auto", nil, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newAutoEngine(t)
			sig := autoSignal(21)
			if tc.mutate != nil {
				tc.mutate(&sig)
			}
			gates := DefaultAutoGates()
			if tc.gates != nil {
				tc.gates(&gates)
			}
			out, err := e.AutoConsume(context.Background(), sig, gates)
			if err != nil {
				t.Fatal(err)
			}
			if out.Skipped != tc.wantSkip {
				t.Fatalf("skipped=%q want %q", out.Skipped, tc.wantSkip)
			}
			intents, err := e.Intents(context.Background(), "SIM")
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantSkip != "" {
				if len(intents) != 0 {
					t.Fatalf("filtered event claimed intent: %+v", intents)
				}
				return
			}
			if out.Plan.State != "authorized" || out.Plan.Mode != "sim_auto" || out.Plan.Operator != "auto" {
				t.Fatalf("plan not authorized as sim_auto: %+v", out.Plan)
			}
			if len(intents) != 1 || intents[0].State != "authorized" || intents[0].BusinessKey != "autosignal:SIM:default:21" {
				t.Fatalf("intent not finished as authorized: %+v", intents)
			}
		})
	}
}

func TestAutoConsumeDuplicateEventSkipsAndCountsIntentOnce(t *testing.T) {
	e, store := newAutoEngine(t)
	ctx := context.Background()
	first, err := e.AutoConsume(ctx, autoSignal(42), DefaultAutoGates())
	if err != nil || first.Skipped != "" {
		t.Fatalf("first consume: out=%+v err=%v", first, err)
	}
	second, err := e.AutoConsume(ctx, autoSignal(42), DefaultAutoGates())
	if err != nil {
		t.Fatal(err)
	}
	if second.Skipped != "duplicate" {
		t.Fatalf("duplicate event not deduped: %+v", second)
	}
	plans, err := store.ListPlans(ctx, BookPaper, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("duplicate event produced extra plan: %d", len(plans))
	}
	intents, err := e.Intents(ctx, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 || intents[0].BusinessKey != "autosignal:SIM:default:42" || intents[0].State != "authorized" || intents[0].Reason != first.Plan.Plan.PlanID {
		t.Fatalf("intents=%+v", intents)
	}
}

// 消费失败必须留下占位意图给人核对（RecoverIntents 会如实标 unknown），
// 而不是静默丢事件，更不自动重发第二次开仓尝试（交易纪律：UNKNOWN 不自动重发）。
func TestAutoConsumeFailureLeavesClaimedIntentForReconciliation(t *testing.T) {
	e, store := newAutoEngine(t)
	store.factsErr = fmt.Errorf("明确标记的合成仓储故障")
	_, err := e.AutoConsume(context.Background(), autoSignal(43), DefaultAutoGates())
	if err == nil {
		t.Fatal("expected failure")
	}
	rows, err := store.ListUnresolvedIntents(context.Background(), BookPaper)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != "claimed" {
		t.Fatalf("unresolved intents missing: %+v", rows)
	}
	again, err := e.AutoConsume(context.Background(), autoSignal(43), DefaultAutoGates())
	if err != nil {
		t.Fatal(err)
	}
	if again.Skipped != "duplicate" {
		t.Fatalf("failure replay must dedupe, got %+v", again)
	}
}
