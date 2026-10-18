package biz

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"server/pkg/accountstate"
	"server/pkg/risk"
	"server/pkg/tradecal"
)

func producedFixture(t *testing.T) accountstate.Snapshot {
	t.Helper()
	raw, err := os.ReadFile("../../../../pkg/accountstate/testdata/trade-produced-full-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	state, err := accountstate.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func consumeFixture(t *testing.T, e *Engine, state accountstate.Snapshot) error {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return e.ConsumeAccount(context.Background(), raw)
}

func TestActualRiskConsumerAcceptsProducedSnapshotAndKeepsIsolation(t *testing.T) {
	r := newRig(t, nil)
	state := producedFixture(t)
	r.clock = state.AsOf
	r.q.m["000001.SZ"] = r.snap(10)
	if err := consumeFixture(t, r.e, state); err != nil {
		t.Fatal(err)
	}
	a := r.e.accounts[risk.SIM]
	if a.Equity != 1_000_000 || a.Available != 941994.92 || a.Holdings["000001.SZ"].Sellable() != 2200 || len(a.Open) != 2 || a.Open[0].Volume != 800 {
		t.Fatal(a)
	}
	if _, ok := r.e.accounts[risk.LIVE]; ok {
		t.Fatal("SIM overwrote LIVE")
	}
	sell := buy("manual-sell", 100)
	sell.Side = risk.Sell
	sell.Source = risk.Manual
	sell.Operator = "test"
	sell.Price = 10.01
	if d, _ := r.e.Check(context.Background(), sell); !d.Approved {
		t.Fatal(d)
	}
	state.AccountType = risk.LIVE
	state.SnapshotID = "live-snapshot"
	if err := consumeFixture(t, r.e, state); err != nil {
		t.Fatal(err)
	}
	if len(r.e.accounts) != 2 {
		t.Fatal("account types were merged")
	}
	wrong := state
	wrong.AccountID = "another-account"
	wrong.Generation++
	if err := consumeFixture(t, r.e, wrong); err == nil {
		t.Fatal("unsupported identity accepted")
	}
}

func TestMalformedOldDuplicateAndExpiredSnapshotsCannotClearLedger(t *testing.T) {
	r := newRig(t, nil)
	state := producedFixture(t)
	r.clock = state.AsOf
	if err := consumeFixture(t, r.e, state); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"book":"paper","halted":true,"diffs":["quantity mismatch"]}`,
		`{"account_type":"SIM","as_of":"2026-10-15T10:00:00+08:00","holdings":[]}`,
		`{"AccountType":"SIM","Equity":1000000}`,
	} {
		if err := r.e.ConsumeAccount(context.Background(), []byte(raw)); err == nil {
			t.Fatal("partial/legacy snapshot accepted")
		}
	}
	for _, name := range []string{"duplicate", "old-generation", "old-time", "expired", "future"} {
		t.Run(name, func(t *testing.T) {
			bad := state
			switch name {
			case "duplicate":
			case "old-generation":
				bad.Generation = state.Generation - 1
				bad.SnapshotID = "old"
			case "old-time":
				bad.Generation++
				bad.SnapshotID = "old-time"
				bad.AsOf = bad.AsOf.Add(-time.Second)
				bad.Reconciliation.AsOf = bad.AsOf
			case "expired":
				bad.Generation++
				bad.SnapshotID = "expired"
				bad.AsOf = bad.AsOf.Add(-2 * time.Minute)
				bad.Reconciliation.AsOf = bad.AsOf
			case "future":
				bad.Generation++
				bad.SnapshotID = "future"
				bad.AsOf = bad.AsOf.Add(time.Minute)
				bad.Reconciliation.AsOf = bad.AsOf
			}
			if err := consumeFixture(t, r.e, bad); err == nil {
				t.Fatal("bad update accepted")
			}
		})
	}
	if len(r.e.accounts[risk.SIM].Holdings) != 1 || r.e.accountStates[risk.SIM].SnapshotID != state.SnapshotID {
		t.Fatal("bad update cleared/overwrote valid state")
	}
}

func TestRecoveryBeforeReadyAndStaleRecoveryFailsClosed(t *testing.T) {
	state := producedFixture(t)
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "stale"}[stale], func(t *testing.T) {
			repo := &fakeRepo{mode: risk.L1, haveMode: true, snapshots: []accountstate.Snapshot{state}}
			r := newRig(t, nil)
			r.e.ready.Store(false)
			r.e.repo = repo
			r.clock = state.AsOf
			if stale {
				r.clock = r.clock.Add(2 * time.Minute)
			}
			r.q.m["000001.SZ"] = r.snap(10)
			if err := r.e.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(r.e.accounts[risk.SIM].Holdings) != 1 {
				t.Fatal("exposure was not restored")
			}
			sell := buy("recover", 100)
			sell.Side = risk.Sell
			sell.Source = risk.Manual
			sell.Operator = "test"
			sell.Price = 10.01
			d, _ := r.e.Check(context.Background(), sell)
			if d.Approved == stale {
				t.Fatal("stale restoration allowed order/fresh state not ready", d)
			}
		})
	}
	r := newRig(t, nil)
	r.e.ready.Store(false)
	r.e.repo = &fakeRepo{accountErr: errors.New("db unavailable")}
	if err := r.e.Start(context.Background()); err == nil || r.e.ready.Load() {
		t.Fatal("failed restore marked risk ready")
	}
}

func TestMissingBaselineBlockedReconciliationAndExpiredAccountRejectOrders(t *testing.T) {
	for _, name := range []string{"baseline", "reconciliation", "expiry", "next-day"} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, nil)
			state := producedFixture(t)
			r.clock = state.AsOf
			state.OpenOrders = []risk.OpenOrder{}
			state.Holdings[0].FrozenSell = 0
			if name == "baseline" {
				state.DayStartKnown = false
				state.DayStartEquity = 0
			}
			if name == "reconciliation" {
				state.Reconciliation.Status = "blocked"
				state.Reconciliation.Reason = "ledger mismatch"
			}
			if err := consumeFixture(t, r.e, state); err != nil {
				t.Fatal(err)
			}
			if name == "expiry" {
				r.clock = r.clock.Add(61 * time.Second)
			}
			if name == "next-day" {
				r.clock = time.Date(2026, 10, 16, 10, 0, 0, 0, tradecal.Shanghai())
			}
			r.q.m["000001.SZ"] = r.snap(9.4)
			sell := buy("guard", 100)
			sell.Side = risk.Sell
			sell.Source = risk.Manual
			sell.Operator = "test"
			sell.Price = 9.41
			if d, _ := r.e.Check(context.Background(), sell); d.Approved || d.Rule != risk.RuleInvalid {
				t.Fatal("unready account allowed order", d)
			}
			r.e.OnSnapshot(context.Background(), SnapshotMetaPayload{})
			if len(r.pub.msgs) != 0 {
				t.Fatal("unready account emitted protective automatic exit")
			}
		})
	}
}

func TestReservationRequiresAuthoritativeOrderObservation(t *testing.T) {
	r := newRig(t, nil)
	r.account(1_000_000, 15000)
	if d, _ := r.e.Check(context.Background(), buy("intent-a", 1000)); !d.Approved {
		t.Fatal(d)
	}
	r.clock = r.clock.Add(61 * time.Second)
	r.q.m["000001.SZ"] = r.snap(10)
	r.account(1_000_000, 15000)
	r.e.Tick(context.Background())
	if _, ok := r.e.reservations["intent-a"]; !ok {
		t.Fatal("TTL silently released unknown order")
	}
	if d, _ := r.e.Check(context.Background(), buy("intent-b", 100)); d.Approved {
		t.Fatal("expired unknown approval allowed new buy", d)
	}
	state := r.e.accountStates[risk.SIM]
	state.Generation++
	state.SnapshotID = "observed-terminal-a"
	state.ObservedOrders = []string{"intent-a"}
	if err := consumeFixture(t, r.e, state); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.e.reservations["intent-a"]; ok {
		t.Fatal("observed terminal order did not release reserve")
	}
	if d, _ := r.e.Check(context.Background(), buy("intent-c", 100)); !d.Approved {
		t.Fatal(d)
	}
}
