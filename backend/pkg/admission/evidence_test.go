package admission

import (
	"slices"
	"testing"
	"time"

	"server/pkg/tradecal"
)

func ptr[T any](v T) *T { return &v }

func localTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04:05", value, tradecal.Shanghai())
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// These are synthetic rule fixtures, never persisted operation evidence.
func syntheticSample(at time.Time) Sample {
	s := Sample{
		Version: Version, Book: "paper", AccountID: "default", InstanceID: "synthetic-rule-test", ClockSource: "server_wall_clock",
		ObservedAt: at, EndedAt: at.Add(100 * time.Millisecond), AccountSnapshotID: "synthetic-snapshot", AccountGeneration: 1,
		AccountAsOf: ptr(at), AccountTradingDay: at.In(tradecal.Shanghai()).Format("2006-01-02"), DayStartKnown: ptr(true),
		LedgerMatched: ptr(true), Reconciliation: "matched", ReconciliationAsOf: ptr(at), RiskReady: ptr(true), RiskMode: "L1",
		KillActive: ptr(false), ConfigReady: ptr(true), RiskConfigRevision: ptr(1), StoredConfigRevision: ptr(1), RiskAccountAsOf: ptr(at),
		UnresolvedIntents: ptr(0), UnknownOrders: ptr(0), OutboxKnown: true, OutboxDead: ptr(0),
		UniverseCount: ptr(5000), UniverseBasis: "synthetic-universe", UniverseKnown: true, MarketRows: ptr(5000), FreshRows: ptr(5000), ClosingRows: ptr(5000),
		MarketOldestSourceTime: ptr(at), MarketSources: map[string]int{"synthetic-source": 5000}, MarketSourceHealthy: ptr(true), CollectionErrors: []string{},
	}
	s.Seal()
	return s
}

func syntheticDay(t *testing.T, d time.Time) []Sample {
	t.Helper()
	d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
	samples := []Sample{}
	for at := d; at.Before(d.AddDate(0, 0, 1)); at = at.Add(SlotWidth) {
		s, err := tradecal.Default.SessionAt(at)
		if err != nil {
			t.Fatal(err)
		}
		if s.Trading {
			samples = append(samples, syntheticSample(at.Add(time.Second)))
		}
	}
	return append(samples, syntheticSample(time.Date(d.Year(), d.Month(), d.Day(), 15, 9, 31, 0, tradecal.Shanghai())))
}

func syntheticDays(t *testing.T, latest time.Time, days int) []Sample {
	t.Helper()
	out := []Sample{}
	d := latest
	for range days {
		out = append(out, syntheticDay(t, d)...)
		var err error
		d, err = tradecal.Default.PrevOpen(d)
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestTwentySyntheticDaysExerciseRulesWithoutGrantingLive(t *testing.T) {
	now := localTime(t, "2026-10-16 16:00:00")
	r, err := Evaluate(tradecal.Default, now, syntheticDays(t, now, 20), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.RequiredDays != MinRealTradingDays || r.ConsecutiveDays != 20 || !r.RunEvidenceReady || r.LiveEnabled || len(r.OtherRequiredEvidence) == 0 {
		t.Fatalf("unexpected admission rule result: %+v", r)
	}
	for _, d := range r.Days[:20] {
		if d.Status != "qualified" || d.RequiredSlots != 480 || d.HealthySlots != 480 || d.Samples != 481 || !d.ClosingProof {
			t.Fatalf("not all AM/PM slots and closing proof required: %+v", d)
		}
	}
}

func TestContinuityStartsAtLatestClosedActualDay(t *testing.T) {
	now := localTime(t, "2026-10-16 16:00:00")
	full := syntheticDays(t, now, 21)
	for _, tc := range []struct {
		name    string
		data    []Sample
		missing int
	}{
		{"missing_latest_day", full[481:], 480},
		{"missing_latest_slot", full[1:], 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Evaluate(tradecal.Default, now, tc.data, 20, 21)
			if err != nil || r.RunEvidenceReady || r.ConsecutiveDays != 0 || r.LatestClosedTradingDay != "2026-10-16" || r.Days[0].MissingSlots != tc.missing {
				t.Fatalf("older good days concealed missing latest evidence: %+v err=%v", r, err)
			}
		})
	}
}

func TestUnhealthyReplicaCannotBeMasked(t *testing.T) {
	now := localTime(t, "2026-10-16 16:00:00")
	for _, closing := range []bool{false, true} {
		for _, badFirst := range []bool{false, true} {
			full := syntheticDay(t, now)
			index := 0
			if closing {
				index = len(full) - 1
			}
			bad := full[index]
			bad.InstanceID = "synthetic-failed-replica"
			bad.RiskReady = ptr(false)
			bad.Seal()
			if badFirst {
				full = append([]Sample{bad}, full...)
			} else {
				full = append(full, bad)
			}
			d, err := evaluateDay(tradecal.Default, localTime(t, "2026-10-16 00:00:00"), now, full)
			if err != nil || d.Status == "qualified" || d.Reasons["risk_unknown_or_stale"] == 0 {
				t.Fatalf("failed replica masked closing=%v badFirst=%v: %+v err=%v", closing, badFirst, d, err)
			}
			if closing && d.ClosingProof || !closing && d.UnhealthySlots != 1 {
				t.Fatalf("failed slot counted healthy: %+v", d)
			}
		}
	}
}

func TestUnknownAndDegradedFactsFailClosed(t *testing.T) {
	at := localTime(t, "2026-10-16 10:00:00")
	for _, tc := range []struct {
		name, reason string
		change       func(*Sample)
	}{
		{"unknown_account", "account_unknown_or_stale", func(s *Sample) { s.AccountAsOf = nil }},
		{"unknown_day_start", "account_day_start_unknown", func(s *Sample) { s.DayStartKnown = nil }},
		{"ledger_difference", "ledger_or_reconciliation_unmatched", func(s *Sample) { s.LedgerMatched = ptr(false) }},
		{"unknown_risk", "risk_unknown_or_stale", func(s *Sample) { s.RiskReady = nil }},
		{"unknown_kill", "killswitch_unknown_or_active", func(s *Sample) { s.KillActive = nil }},
		{"unknown_config", "configuration_not_loaded", func(s *Sample) { s.RiskConfigRevision = nil }},
		{"wrong_config", "configuration_not_loaded", func(s *Sample) { s.RiskConfigRevision = ptr(2) }},
		{"wrong_mode", "sim_mode_not_active", func(s *Sample) { s.RiskMode = "L0" }},
		{"unknown_intents", "unresolved_execution_identity", func(s *Sample) { s.UnresolvedIntents = nil }},
		{"unknown_orders", "unresolved_execution_identity", func(s *Sample) { s.UnknownOrders = ptr(1) }},
		{"unknown_outbox", "outbox_unavailable_or_backlogged", func(s *Sample) { s.OutboxKnown = false }},
		{"dead_outbox", "outbox_unavailable_or_backlogged", func(s *Sample) { s.OutboxDead = ptr(1) }},
		{"old_outbox", "outbox_unavailable_or_backlogged", func(s *Sample) { s.OutboxOldest = ptr(at.Add(-31 * time.Second)) }},
		{"unknown_universe", "market_universe_unknown", func(s *Sample) { s.UniverseKnown = false }},
		{"sparse_quotes", "market_coverage_insufficient", func(s *Sample) { s.MarketRows = ptr(4749) }},
		{"stale_quotes", "market_source_time_stale", func(s *Sample) { s.MarketOldestSourceTime = ptr(at.Add(-11 * time.Second)) }},
		{"unknown_source", "market_source_unknown", func(s *Sample) { s.MarketSourceHealthy = nil }},
		{"source_count_mismatch", "market_source_unknown", func(s *Sample) { s.MarketSources = map[string]int{"synthetic-source": 4999} }},
		{"dependency_failure", "collection_dependency_error", func(s *Sample) { s.CollectionErrors = []string{"synthetic-error"} }},
		{"slow_sample", "collection_too_slow", func(s *Sample) { s.EndedAt = at.Add(4 * time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := syntheticSample(at)
			tc.change(&s)
			s.Seal()
			if got := s.Problems(true); !slices.Contains(got, tc.reason) {
				t.Fatalf("missing %q in %v", tc.reason, got)
			}
		})
	}
	baseline := syntheticSample(at)
	if got := baseline.Problems(true); len(got) != 0 {
		t.Fatalf("bad synthetic baseline: %v", got)
	}
}

func TestTamperFutureAndExactClosingWindow(t *testing.T) {
	now := localTime(t, "2026-10-16 16:00:00")
	for _, tc := range []struct {
		name, reason string
		change       func([]Sample)
	}{
		{"tampered_hash", "invalid_evidence_identity", func(s []Sample) { s[0].AccountGeneration++ }},
		{"future_completion", "future_evidence", func(s []Sample) { s[0].EndedAt = now.Add(time.Minute); s[0].Seal() }},
		{"early_close", "closing_proof_missing", func(s []Sample) { s[len(s)-1] = syntheticSample(localTime(t, "2026-10-16 15:09:29")) }},
		{"unknown_closing_rows", "closing_market_evidence_missing", func(s []Sample) { s[len(s)-1].ClosingRows = nil; s[len(s)-1].Seal() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full := syntheticDay(t, now)
			tc.change(full)
			d, err := evaluateDay(tradecal.Default, localTime(t, "2026-10-16 00:00:00"), now, full)
			if err != nil || d.Status == "qualified" || d.Reasons[tc.reason] == 0 {
				t.Fatalf("bad evidence accepted: %+v err=%v", d, err)
			}
		})
	}
}

func TestClosedDayAnchorCalendarAndFinalization(t *testing.T) {
	latest := localTime(t, "2026-10-16 16:00:00")
	data := syntheticDay(t, latest)
	for _, tc := range []struct {
		now, anchor string
		consecutive int
	}{
		{"2026-10-16 15:09:59", "2026-10-15", 0},
		{"2026-10-16 15:10:00", "2026-10-16", 1},
		{"2026-10-18 16:00:00", "2026-10-16", 1},
		{"2026-10-07 16:00:00", "2026-09-30", 0},
	} {
		r, err := Evaluate(tradecal.Default, localTime(t, tc.now), data, 20, 21)
		if err != nil || r.LatestClosedTradingDay != tc.anchor || r.ConsecutiveDays != tc.consecutive {
			t.Fatalf("clock/calendar anchor incorrect: %+v err=%v", r, err)
		}
		if tc.now == "2026-10-16 15:09:59" && r.Days[0].Status != "running" {
			t.Fatal("unfinished day must remain running")
		}
	}
	if _, err := Evaluate(tradecal.Default, localTime(t, "2027-01-05 16:00:00"), nil, 20, 21); err == nil {
		t.Fatal("unknown future holiday calendar must fail closed")
	}
}
