package decision

import (
	"server/pkg/accountstate"
	"server/pkg/market"
	"server/pkg/risk"
	"testing"
	"time"
)

func fixture() (Snapshot, Plan, accountstate.Snapshot, time.Time) {
	now := time.Date(2026, 10, 16, 2, 0, 0, 0, time.UTC)
	a := accountstate.Snapshot{Version: 1, Kind: "full", AccountID: "default", AccountType: risk.AccountType("SIM"), SnapshotID: "account-1", Generation: 1, AsOf: now, TradingDay: "2026-10-16", Cash: 100000, Available: 100000, Equity: 100000, DayStartKnown: true, DayStartEquity: 100000, Holdings: []risk.Holding{}, OpenOrders: []risk.OpenOrder{}, ObservedOrders: []string{}, Reconciliation: accountstate.Reconciliation{Status: "matched", AsOf: now}}
	q := market.Snapshot{Symbol: "600519.SH", Time: now, AsOf: now, Last: 10}
	s := Snapshot{Version: 1, SnapshotID: "facts-1", SignalID: 1, SignalHash: "signalhash", Symbol: q.Symbol, AccountType: "SIM", AccountID: "default", AccountSnapshotID: a.SnapshotID, AccountGeneration: 1, AccountStateHash: AccountHash(a), ConfigRevision: 2, FormedAt: now, SourceTime: now, Quote: q, RuleScore: 80}
	p := Plan{Version: 1, PlanID: "plan-1", Revision: 1, SnapshotID: s.SnapshotID, SignalID: 1, AccountType: "SIM", AccountID: "default", Symbol: q.Symbol, Side: "buy", PriceOrigin: "rules", EntryLow: 9.8, EntryHigh: 10.2, LimitPrice: 10, StopLoss: 9, TakeProfit: 11, PositionPct: .05, MaxNotional: 5000, ValidFrom: now, ValidUntil: now.Add(20 * time.Minute), ExecutionDay: "2026-10-16"}
	return s, p, a, now
}
func TestPlanCurrentFactsAndInvalidation(t *testing.T) {
	s, p, a, now := fixture()
	if err := Eligible(p, s, a, s.Quote, s.SignalHash, 2, now); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Plan, *Snapshot, *accountstate.Snapshot, *market.Snapshot, *string, *int, *time.Time){
		"expired": func(p *Plan, _ *Snapshot, _ *accountstate.Snapshot, _ *market.Snapshot, _ *string, _ *int, n *time.Time) {
			*n = p.ValidUntil
		},
		"config": func(_ *Plan, _ *Snapshot, _ *accountstate.Snapshot, _ *market.Snapshot, _ *string, r *int, _ *time.Time) {
			*r = 3
		},
		"signal": func(_ *Plan, _ *Snapshot, _ *accountstate.Snapshot, _ *market.Snapshot, h *string, _ *int, _ *time.Time) {
			*h = "changed"
		},
		"wrong account": func(_ *Plan, _ *Snapshot, a *accountstate.Snapshot, _ *market.Snapshot, _ *string, _ *int, _ *time.Time) {
			a.AccountType = risk.AccountType("LIVE")
		},
		"ledger": func(_ *Plan, _ *Snapshot, a *accountstate.Snapshot, _ *market.Snapshot, _ *string, _ *int, _ *time.Time) {
			a.Cash -= 100
			a.Available -= 100
			a.Equity -= 100
		},
		"stale quote": func(_ *Plan, _ *Snapshot, _ *accountstate.Snapshot, q *market.Snapshot, _ *string, _ *int, n *time.Time) {
			q.Time = n.Add(-11 * time.Second)
		},
		"not triggered": func(_ *Plan, _ *Snapshot, _ *accountstate.Snapshot, q *market.Snapshot, _ *string, _ *int, _ *time.Time) {
			q.Last = 10.3
		},
		"model price": func(p *Plan, _ *Snapshot, _ *accountstate.Snapshot, _ *market.Snapshot, _ *string, _ *int, _ *time.Time) {
			p.PriceOrigin = "model"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s, p, a, now := fixture()
			q, h, revision := s.Quote, s.SignalHash, 2
			mutate(&p, &s, &a, &q, &h, &revision, &now)
			if Eligible(p, s, a, q, h, revision, now) == nil {
				t.Fatal("invalid facts accepted")
			}
		})
	}
}
func TestHeartbeatKeepsEconomicIdentity(t *testing.T) {
	s, p, a, now := fixture()
	a.Generation++
	a.SnapshotID = "account-2"
	a.AsOf = now.Add(time.Second)
	a.Reconciliation.AsOf = a.AsOf
	if AccountHash(a) != s.AccountStateHash {
		t.Fatal("heartbeat changed ledger")
	}
	if err := Eligible(p, s, a, s.Quote, s.SignalHash, 2, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	a.ObservedOrders = append(a.ObservedOrders, "new-order")
	if AccountHash(a) == s.AccountStateHash {
		t.Fatal("order did not invalidate ledger")
	}
}
