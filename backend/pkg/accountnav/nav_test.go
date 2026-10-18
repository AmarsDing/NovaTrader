package accountnav

import (
	"encoding/json"
	"server/pkg/accountstate"
	"server/pkg/dataset"
	"server/pkg/risk"
	"server/pkg/tradecal"
	"strings"
	"testing"
	"time"
)

func testTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04:05", value, tradecal.Shanghai())
	if err != nil {
		t.Fatal(err)
	}
	return at
}
func snapshotRow(t *testing.T, id int, at time.Time, cash, mark float64) SnapshotRow {
	t.Helper()
	s := accountstate.Snapshot{Version: 1, Kind: "full", AccountID: "default", AccountType: risk.SIM, SnapshotID: "synthetic-" + at.Format("20060102"), Generation: int64(id), AsOf: at, TradingDay: at.In(tradecal.Shanghai()).Format("2006-01-02"), Cash: cash, Available: cash, Equity: cash, DayStartEquity: 100000, DayStartKnown: true, Holdings: []risk.Holding{}, OpenOrders: []risk.OpenOrder{}, ObservedOrders: []string{}, Reconciliation: accountstate.Reconciliation{Status: "matched", AsOf: at}}
	if mark > 0 {
		s.Holdings = []risk.Holding{{Symbol: "600519.SH", Quantity: 100, Available: 100, AvgCost: 10, MarketValue: 100 * mark}}
		s.Equity += 100 * mark
		s.ObservedOrders = []string{"synthetic-buy"}
		day, _ := date(s.TradingDay)
		start := day.Add(14*time.Hour + 59*time.Minute)
		s.Valuation = &accountstate.ValuationProof{Version: 1, Source: "market.validated_minute_bar", Complete: true, Marks: []accountstate.ValuationMark{{Symbol: "600519.SH", Quantity: 100, Price: mark, BarStart: start, BarEnd: start.Add(time.Minute)}}, Reasons: []string{}}
	}
	p, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Validate(); err != nil {
		t.Fatal(err)
	}
	return SnapshotRow{ID: id, Book: "paper", At: at, State: p}
}
func fixture(t *testing.T) (Query, Evidence) {
	t.Helper()
	cutoff := testTime(t, "2026-10-15 16:00:00")
	calendar := []time.Time{testTime(t, "2026-10-13 00:00:00"), testTime(t, "2026-10-14 00:00:00"), testTime(t, "2026-10-15 00:00:00")}
	m, err := dataset.Freeze(dataset.Request{From: calendar[0], To: calendar[2], Cutoff: cutoff, Freq: "1d"}, calendar, nil)
	if err != nil {
		t.Fatal(err)
	}
	q, err := Normalize(Request{Account: "SIM", StartDate: "2026-10-14", EndDate: "2026-10-15", Frequency: "1d", AsOf: cutoff, ManifestID: m.ManifestID, BenchmarkJSON: `{"benchmark_id":"synthetic-explicit-index","benchmark_kind":"price_index","points":[{"date":"2026-10-13","value":100},{"date":"2026-10-14","value":101},{"date":"2026-10-15","value":102}]}`}, cutoff, tradecal.Default)
	if err != nil {
		t.Fatal(err)
	}
	e := Evidence{Manifest: m, Snapshots: []SnapshotRow{
		snapshotRow(t, 101, testTime(t, "2026-10-13 15:09:50"), 100000, 0),
		snapshotRow(t, 102, testTime(t, "2026-10-14 15:09:50"), 98995, 10.1),
		snapshotRow(t, 103, testTime(t, "2026-10-15 15:09:50"), 98995, 10.2),
	}, Fills: []Fill{{ID: 1, Book: "paper", ClientOrderID: "synthetic-buy", Symbol: "600519.SH", Side: "buy", Qty: 100, Price: 10, Amount: 1000, Fee: 5, At: testTime(t, "2026-10-14 10:00:00"), OriginalOrderKnown: true}}}
	return q, e
}
func mutateState(t *testing.T, row *SnapshotRow, change func(*accountstate.Snapshot)) {
	t.Helper()
	var s accountstate.Snapshot
	if err := json.Unmarshal(row.State, &s); err != nil {
		t.Fatal(err)
	}
	change(&s)
	p, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	row.State = p
}

func TestFeeNetNAVIncludesActualInitialCloseAndExactWorkerInput(t *testing.T) {
	q, e := fixture(t)
	r := Build(q, e)
	if !r.InputReady {
		t.Fatalf("valid synthetic ledger rejected: %v", r.Report.Reasons)
	}
	if r.Report.InitialDate != "2026-10-13" || r.Report.Points[0].Role != "initial" || r.Report.CashFlowPolicy != "no_external_flows" || *r.Report.Points[1].Equity != 100005 || *r.Report.Points[2].Equity != 100015 {
		t.Fatalf("fees or comparison point wrong: %+v", r.Report)
	}
	if *r.Report.Points[1].NormalizedNAV != 1.00005 || *r.Report.Points[2].NormalizedNAV != 1.00015 {
		t.Fatal("normalized NAV did not include real fees")
	}
	var input workerInput
	if err := json.Unmarshal([]byte(r.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	if input.Points[0].NAV != 100000 || input.Points[1].NAV != 100005 || input.Points[1].Generation != "102" || input.Kind != "account_nav" || r.InputSHA256 != hash([]byte(r.InputJSON)) {
		t.Fatal("worker input mismatch", input)
	}
	if strings.Contains(r.InputJSON, "account_id") || strings.Contains(r.InputJSON, "signal_return") {
		t.Fatal("research input leaked account identity or used signal samples")
	}
	if r2 := Build(q, e); r2.InputSHA256 != r.InputSHA256 || r2.Report.LedgerHash != r.Report.LedgerHash {
		t.Fatal("same frozen evidence did not reproduce")
	}
}

func TestMissingOrInvalidClosingEvidenceNeverBecomesZeroNAV(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Query, *Evidence)
	}{
		{"missing_initial", func(q *Query, e *Evidence) { e.Snapshots = e.Snapshots[1:] }},
		{"missing_day", func(q *Query, e *Evidence) { e.Snapshots = append(e.Snapshots[:1], e.Snapshots[2:]...) }},
		{"latest_legacy_not_fallback", func(q *Query, e *Evidence) {
			row := e.Snapshots[2]
			row.ID++
			row.At = row.At.Add(time.Second)
			row.State = nil
			e.Snapshots = append(e.Snapshots, row)
		}},
		{"wrong_generation", func(q *Query, e *Evidence) {
			mutateState(t, &e.Snapshots[1], func(s *accountstate.Snapshot) { s.Generation = 999 })
		}},
		{"other_account", func(q *Query, e *Evidence) {
			mutateState(t, &e.Snapshots[1], func(s *accountstate.Snapshot) { s.AccountType = risk.LIVE })
		}},
		{"old_valuation_unknown", func(q *Query, e *Evidence) {
			mutateState(t, &e.Snapshots[1], func(s *accountstate.Snapshot) { s.Valuation = nil })
		}},
		{"not_closing_mark", func(q *Query, e *Evidence) {
			mutateState(t, &e.Snapshots[1], func(s *accountstate.Snapshot) {
				s.Valuation.Marks[0].BarStart = s.Valuation.Marks[0].BarStart.Add(-time.Minute)
				s.Valuation.Marks[0].BarEnd = s.Valuation.Marks[0].BarEnd.Add(-time.Minute)
			})
		}},
		{"unreconciled", func(q *Query, e *Evidence) {
			mutateState(t, &e.Snapshots[1], func(s *accountstate.Snapshot) {
				s.Reconciliation.Status = "blocked"
				s.Reconciliation.Reason = "synthetic difference"
			})
		}},
		{"daystart_unknown", func(q *Query, e *Evidence) {
			mutateState(t, &e.Snapshots[1], func(s *accountstate.Snapshot) { s.DayStartKnown = false; s.DayStartEquity = 0 })
		}},
		{"duplicate_snapshot_identity", func(q *Query, e *Evidence) {
			mutateState(t, &e.Snapshots[1], func(s *accountstate.Snapshot) { s.SnapshotID = "synthetic-20261013" })
		}},
		{"unclosed_day", func(q *Query, e *Evidence) { q.AsOf = testTime(t, "2026-10-15 15:09:59") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, e := fixture(t)
			tc.change(&q, &e)
			r := Build(q, e)
			if r.InputReady || r.InputJSON != "" || len(r.Report.Reasons) == 0 {
				t.Fatalf("incomplete ledger admitted: %+v", r)
			}
		})
	}
	q, e := fixture(t)
	e.Snapshots = e.Snapshots[1:]
	r := Build(q, e)
	if r.Report.Points[0].Equity != nil || r.Report.Points[0].NormalizedNAV != nil {
		t.Fatal("missing initial snapshot was filled with zero or invented NAV")
	}
}

func TestCashChangesMustReconcileToActualFillCosts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Evidence)
	}{
		{"missing_fill", func(e *Evidence) { e.Fills = nil }},
		{"fee_not_included", func(e *Evidence) { e.Fills[0].Fee = 0 }},
		{"unknown_original_order", func(e *Evidence) { e.Fills[0].OriginalOrderKnown = false }},
		{"wrong_book", func(e *Evidence) { e.Fills[0].Book = "live" }},
		{"duplicate_fill", func(e *Evidence) { e.Fills = append(e.Fills, e.Fills[0]) }},
		{"unknown_fill_order", func(e *Evidence) { e.Fills[0].ClientOrderID = "not-observed" }},
		{"unexplained_deposit", func(e *Evidence) {
			mutateState(t, &e.Snapshots[1], func(s *accountstate.Snapshot) { s.Cash += 100; s.Available += 100; s.Equity += 100 })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, e := fixture(t)
			tc.change(&e)
			r := Build(q, e)
			if r.InputReady || r.Report.CashFlowPolicy != "unknown" || r.Report.Points[1].ExternalFlow != nil {
				t.Fatalf("unknown cash flow admitted or filled with zero: %+v", r.Report)
			}
		})
	}
}

func TestBenchmarkAndManifestAreExplicitAndAligned(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Query, *Evidence)
	}{
		{"benchmark_missing", func(q *Query, e *Evidence) { q.BenchmarkJSON = "" }},
		{"benchmark_download_forbidden", func(q *Query, e *Evidence) {
			q.BenchmarkJSON = `{"benchmark_id":"000300.SH","benchmark_kind":"price_index","ticker":"000300.SH","points":[]}`
		}},
		{"benchmark_date_mismatch", func(q *Query, e *Evidence) {
			q.BenchmarkJSON = strings.Replace(q.BenchmarkJSON, "2026-10-14", "2026-10-16", 1)
		}},
		{"benchmark_nonpositive", func(q *Query, e *Evidence) {
			q.BenchmarkJSON = strings.Replace(q.BenchmarkJSON, `"value":101`, `"value":0`, 1)
		}},
		{"manifest_missing", func(q *Query, e *Evidence) { e.Manifest = nil }},
		{"manifest_mismatch", func(q *Query, e *Evidence) { q.ManifestID = "different" }},
		{"manifest_calendar_gap", func(q *Query, e *Evidence) {
			m, err := dataset.Freeze(e.Manifest.Request, e.Manifest.Calendar[1:], nil)
			if err != nil {
				t.Fatal(err)
			}
			e.Manifest = m
			q.ManifestID = m.ManifestID
		}},
		{"manifest_before_observation", func(q *Query, e *Evidence) {
			req := e.Manifest.Request
			req.Cutoff = testTime(t, "2026-10-15 15:05:00")
			m, err := dataset.Freeze(req, e.Manifest.Calendar, nil)
			if err != nil {
				t.Fatal(err)
			}
			e.Manifest = m
			q.ManifestID = m.ManifestID
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, e := fixture(t)
			tc.change(&q, &e)
			if r := Build(q, e); r.InputReady || r.InputJSON != "" {
				t.Fatal("unfrozen benchmark/manifest admitted")
			}
		})
	}
}

func TestRequestCannotExportLiveOrFabricatedFrequency(t *testing.T) {
	now := testTime(t, "2026-10-15 16:00:00")
	for _, req := range []Request{
		{Account: "LIVE", StartDate: "2026-10-14", EndDate: "2026-10-15"},
		{Account: "SIM", StartDate: "2026-10-14", EndDate: "2026-10-15", Frequency: "1m"},
		{Account: "SIM", StartDate: "2026-10-15", EndDate: "2026-10-14"},
		{Account: "SIM", StartDate: "2027-01-05", EndDate: "2027-01-06"},
		{Account: "SIM", StartDate: "2026-10-14", EndDate: "2026-10-15", AsOf: now.Add(time.Hour)},
	} {
		if _, err := Normalize(req, now, tradecal.Default); err == nil {
			t.Fatalf("invalid request admitted: %+v", req)
		}
	}
}

func TestSnapshotTimestampAllowsOnlyPostgresMicrosecondPrecision(t *testing.T){
	q,e:=fixture(t);e.Snapshots[1].At=e.Snapshots[1].At.Add(500*time.Nanosecond)
	if r:=Build(q,e);!r.InputReady{t.Fatalf("known timestamp precision rejected: %v",r.Report.Reasons)}
	e.Snapshots[1].At=e.Snapshots[1].At.Add(time.Millisecond)
	if r:=Build(q,e);r.InputReady{t.Fatal("genuine source/index timestamp mismatch accepted")}
	if r:=Build(Query{},Evidence{});r.InputReady || len(r.Report.Reasons)==0{t.Fatal("unnormalized empty query admitted")}
}
