package biz

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"server/pkg/accountstate"
	"server/pkg/events"
	"server/pkg/risk"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

var accountTime = time.Date(2026, 10, 15, 10, 0, 0, 0, tradecal.Shanghai())

func latestState(t *testing.T, mem *MemStore) accountstate.Snapshot {
	t.Helper()
	eventsList := mem.Events()
	for i := len(eventsList) - 1; i >= 0; i-- {
		if eventsList[i].Subject == events.SubjectTradeAccount {
			return eventsList[i].Payload.(accountstate.Snapshot)
		}
	}
	t.Fatal("missing full snapshot event")
	return accountstate.Snapshot{}
}

func TestActualSnapshotProducerMatchesConsumerFixture(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.now = func() time.Time { return accountTime }
	ctx := context.Background()
	if err := mem.Commit(ctx, Batch{
		Cash:      &Cash{Book: BookPaper, Cash: 950000, DayStartDate: "2026-10-15", DayStartEquity: 1_000_000},
		Positions: []Position{{Book: BookPaper, Symbol: "000001.SZ", Quantity: 5000, Available: 3000, AvgCost: 9.5, LastPrice: 10}},
		Orders: []Order{
			{ClientOrderID: "b1", Book: BookPaper, Symbol: "000001.SZ", Side: "buy", Status: StatusPartial, Price: 10, Volume: 1000, Filled: 200, CreatedAt: accountTime.Add(-2 * time.Second)},
			{ClientOrderID: "s1", Book: BookPaper, Symbol: "000001.SZ", Side: "sell", Status: StatusPartial, Price: 10.2, Volume: 1000, Filled: 200, CreatedAt: accountTime.Add(-time.Second)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshot(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	state := latestState(t, mem)
	state.SnapshotID = "fixture-snapshot-1" // the only nondeterministic identity
	env, err := events.NewWithContext(ctx, "trade", events.SubjectTradeAccount, state)
	if err != nil {
		t.Fatal(err)
	}
	produced, err := accountstate.Decode(env.Payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../../pkg/accountstate/testdata/trade-produced-full-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := accountstate.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(produced, want) {
		t.Fatalf("producer drifted from shared consumer fixture:\ngot %+v\nwant %+v", produced, want)
	}
}

func TestBaselineGenerationSurviveEngineRestart(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.now = func() time.Time { return accountTime }
	ctx := context.Background()
	if _, err := e.Snapshot(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	first := latestState(t, mem)
	cash, _, _ := mem.LoadCash(ctx, BookPaper)
	cash.Cash -= 1000
	if err := mem.Commit(ctx, Batch{Cash: &cash}); err != nil {
		t.Fatal(err)
	}
	restarted := NewEngine(mem, allow{}, Settings{}, log.DefaultLogger)
	restarted.now = func() time.Time { return accountTime.Add(time.Hour) }
	if _, err := restarted.Snapshot(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	second := latestState(t, mem)
	if second.Generation <= first.Generation || second.DayStartEquity != 1_000_000 || second.Equity != 999000 {
		t.Fatalf("restart reset baseline/generation: %+v", second)
	}
	if _, err := restarted.Snapshot(ctx, "LIVE"); err != nil {
		t.Fatal(err)
	}
	live := latestState(t, mem)
	if live.Cash != 0 || live.AccountType != risk.LIVE || live.Ready(live.AsOf, time.Minute) {
		t.Fatal("unconnected LIVE was fabricated", live)
	}
}

func TestHoldingMarksContinueAfterOrderIsFilled(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.now = func() time.Time { return accountTime }
	ctx := context.Background()
	q := bar()
	if _, err := e.Place(ctx, PlaceRequest{ClientOrderID: "marked", Account: "SIM", Symbol: "000001.SZ", Side: "buy", Price: 10.05, Volume: 100, Quote: &q}); err != nil {
		t.Fatal(err)
	}
	q.Close = 10.5
	if _, err := e.ApplyBar(ctx, "SIM", "000001.SZ", q); err != nil {
		t.Fatal(err)
	}
	state := latestStateAfterSnapshot(t, e, mem)
	if len(state.Holdings) != 1 || state.Holdings[0].MarketValue != 1050 || state.DayStartEquity != 1_000_000 {
		t.Fatal("filled position stopped marking", state)
	}
}

func TestOpenOrderFreezesAndPartialFillsConserveLedger(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.now = func() time.Time { return accountTime }
	ctx := context.Background()
	if err := mem.Commit(ctx, Batch{Cash: &Cash{Book: BookPaper, Cash: 2500, DayStartDate: "2026-10-15", DayStartEquity: 2500}}); err != nil {
		t.Fatal(err)
	}
	q := bar()
	q.Volume = 1000
	if o, err := e.Place(ctx, PlaceRequest{ClientOrderID: "part", Account: "SIM", Symbol: "000001.SZ", Side: "buy", Price: 10.05, Volume: 200, Quote: &q}); err != nil || o.Filled != 100 {
		t.Fatal(o, err)
	}
	if _, err := e.Snapshot(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	state := latestState(t, mem)
	if len(state.OpenOrders) != 1 || state.OpenOrders[0].Volume != 100 || state.Holdings[0].Quantity != 100 || state.Holdings[0].Available != 0 || state.FrozenCash <= 1005 {
		t.Fatal(state)
	}
	if o, err := e.Place(ctx, PlaceRequest{ClientOrderID: "too-much", Account: "SIM", Symbol: "000001.SZ", Side: "buy", Price: 10, Volume: 100}); err != nil || o.Status != StatusRejected {
		t.Fatal("spent frozen cash", o, err)
	}
	if _, err := e.ApplyBar(ctx, "SIM", "000001.SZ", q); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshot(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	state = latestState(t, mem)
	if state.Holdings[0].Quantity != 200 || state.Holdings[0].Available != 0 || len(state.OpenOrders) != 0 || state.FrozenCash != 0 {
		t.Fatal("partial fill lost quantities or released T+1", state)
	}
}

func TestSellFreezeAndReconciliationAreSeparate(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.now = func() time.Time { return accountTime }
	ctx := context.Background()
	if err := mem.Commit(ctx, Batch{Cash: &Cash{Book: BookPaper, Cash: 100000, DayStartDate: "2026-10-15", DayStartEquity: 105000}, Positions: []Position{{Book: BookPaper, Symbol: "000001.SZ", Quantity: 500, Available: 300, AvgCost: 10, LastPrice: 10}}}); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"s1", "s2"} {
		o, err := e.Place(ctx, PlaceRequest{ClientOrderID: id, Account: "SIM", Symbol: "000001.SZ", Side: "sell", Price: 10, Volume: 200})
		if err != nil || (i == 0 && o.Status != StatusSubmitted) || (i == 1 && o.Status != StatusRejected) {
			t.Fatal("sell freeze ignored", o, err)
		}
	}
	if _, err := e.Snapshot(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	state := latestState(t, mem)
	if state.Holdings[0].FrozenSell != 200 || state.Holdings[0].Sellable() != 100 {
		t.Fatal(state)
	}
	count := len(mem.Events())
	if ok, _, err := e.Reconcile(ctx, "SIM", nil); err != nil || ok {
		t.Fatal(ok, err)
	}
	for _, ev := range mem.Events()[count:] {
		if ev.Subject == events.SubjectTradeAccount {
			t.Fatal("reconciliation delta masqueraded as full state")
		}
	}
	if _, err := e.Snapshot(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	if state := latestState(t, mem); state.Reconciliation.Status != "blocked" || len(state.Holdings) != 1 {
		t.Fatal("reconciliation cleared positions", state)
	}
	if _, err := e.Cancel(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshot(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	if state := latestState(t, mem); state.Holdings[0].FrozenSell != 0 {
		t.Fatal("cancel failed to release shares", state)
	}
}

type brokenSnapshotStore struct{ *MemStore }

func (m brokenSnapshotStore) CommitSnapshot(context.Context, string, AccountView, Cash, accountstate.Snapshot) (accountstate.Snapshot, error) {
	return accountstate.Snapshot{}, errors.New("storage unavailable")
}

func TestFailedSnapshotPublishesNothingAndUnknownBaselineStaysUnknown(t *testing.T) {
	mem := NewMemStore()
	e := NewEngine(brokenSnapshotStore{mem}, allow{}, Settings{}, log.DefaultLogger)
	if _, err := e.Snapshot(context.Background(), "SIM"); err == nil || len(mem.Events()) != 0 {
		t.Fatal("failed commit emitted snapshot")
	}
	e = NewEngine(mem, allow{}, Settings{}, log.DefaultLogger)
	e.now = func() time.Time { return accountTime }
	if err := mem.Commit(context.Background(), Batch{Cash: &Cash{Book: BookPaper, Cash: 900000}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshot(context.Background(), "SIM"); err != nil {
		t.Fatal(err)
	}
	state := latestState(t, mem)
	if state.DayStartKnown || state.DayStartEquity != 0 || state.Ready(accountTime, time.Minute) {
		t.Fatal("legacy baseline fabricated", state)
	}
}

func TestSameDayRollCannotUnlockT1OrAccruePaperDays(t *testing.T) {
	e, mem := newEngine(t, allow{})
	now := accountTime
	e.now = func() time.Time { return now }
	ctx := context.Background()
	q := bar()
	if _, err := e.Place(ctx, PlaceRequest{ClientOrderID: "buy", Account: "SIM", Symbol: "000001.SZ", Side: "buy", Price: 10.05, Volume: 100, Quote: &q}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := e.RollDay(ctx, "SIM"); err != nil {
			t.Fatal(err)
		}
	}
	cash, _, _ := mem.LoadCash(ctx, BookPaper)
	pos, _, _ := mem.LoadPosition(ctx, BookPaper, "000001.SZ")
	if pos.Available != 0 || cash.PaperDays != 0 {
		t.Fatal("same-day rollover bypassed T+1", cash, pos)
	}
	now = now.AddDate(0, 0, 1)
	if _, err := e.RollDay(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RollDay(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	cash, _, _ = mem.LoadCash(ctx, BookPaper)
	pos, _, _ = mem.LoadPosition(ctx, BookPaper, "000001.SZ")
	if pos.Available != 100 || cash.PaperDays != 1 || cash.DayStartDate != "2026-10-16" {
		t.Fatal(cash, pos)
	}
	if _, err := json.Marshal(latestStateAfterSnapshot(t, e, mem)); err != nil {
		t.Fatal(err)
	}
}

func TestFirstNewDayBarSettlesOnlyOldSharesBeforeMatching(t *testing.T) {
	e, mem := newEngine(t, allow{})
	now := accountTime
	e.now = func() time.Time { return now }
	ctx := context.Background()
	q := bar()
	if _, err := e.Place(ctx, PlaceRequest{ClientOrderID: "day-one-fill", Account: "SIM", Symbol: "000001.SZ", Side: "buy", Price: 10.05, Volume: 100, Quote: &q}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Place(ctx, PlaceRequest{ClientOrderID: "overnight-working", Account: "SIM", Symbol: "000001.SZ", Side: "buy", Price: 10.05, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	prior := latestStateAfterSnapshot(t, e, mem)
	now = now.AddDate(0, 0, 1)
	q.Close = 10.5
	if _, err := e.ApplyBar(ctx, "SIM", "000001.SZ", q); err != nil {
		t.Fatal(err)
	}
	state := latestStateAfterSnapshot(t, e, mem)
	if state.Holdings[0].Quantity != 200 || state.Holdings[0].Available != 100 || state.DayStartEquity != prior.Equity {
		t.Fatal("rollover unlocked today's fill or reset baseline after marking", state, prior)
	}
	cash, _, _ := mem.LoadCash(ctx, BookPaper)
	if cash.PaperDays != 0 {
		t.Fatal("automatic rollover fabricated continuous-day admission evidence")
	}
}

func TestExistingCurrentDayFillWithoutBaselineCannotBeSettled(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.now = func() time.Time { return accountTime }
	ctx := context.Background()
	if err := mem.Commit(ctx, Batch{Cash: &Cash{Book: BookPaper, Cash: 100000, DayStartDate: "2026-10-14", DayStartEquity: 100000}, Fills: []Fill{{ClientOrderID: "legacy-today", Book: BookPaper, Symbol: "000001.SZ", Qty: 100, CreatedAt: accountTime}}, Positions: []Position{{Book: BookPaper, Symbol: "000001.SZ", Quantity: 100, Available: 0, AvgCost: 10, LastPrice: 10}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RollDay(ctx, "SIM"); err == nil {
		t.Fatal("intraday unknown baseline was silently reset")
	}
	if _, err := e.ApplyBar(ctx, "SIM", "000001.SZ", bar()); err == nil {
		t.Fatal("unreconciled account kept matching")
	}
	p, _, _ := mem.LoadPosition(ctx, BookPaper, "000001.SZ")
	if p.Available != 0 {
		t.Fatal("current day fill became sellable")
	}
}

func latestStateAfterSnapshot(t *testing.T, e *Engine, mem *MemStore) accountstate.Snapshot {
	t.Helper()
	if _, err := e.Snapshot(context.Background(), "SIM"); err != nil {
		t.Fatal(err)
	}
	return latestState(t, mem)
}
