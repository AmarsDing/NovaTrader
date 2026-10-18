// Package accountnav exports observed fee-net SIM equity without inventing
// closing valuations, initial prices, external flows or benchmark observations.
package accountnav

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"time"

	"server/pkg/accountstate"
	"server/pkg/dataset"
	"server/pkg/risk"
	"server/pkg/tradecal"
)

var ErrInvalid = errors.New("account NAV: invalid request")
var manifestIdentity = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Request struct {
	Account, StartDate, EndDate, Frequency, ManifestID, BenchmarkJSON string
	AsOf                                                              time.Time
}
type Query struct {
	Request
	ExpectedDates []string
	InitialDate   string
	From, To      time.Time
}
type SnapshotRow struct {
	ID    int
	Book  string
	At    time.Time
	State json.RawMessage
}
type Fill struct {
	ID                                int
	Book, ClientOrderID, Symbol, Side string
	Qty                               int
	Price, Amount, Fee                float64
	At                                time.Time
	OriginalOrderKnown                bool
}
type Evidence struct {
	Snapshots     []SnapshotRow
	Fills         []Fill
	Manifest      *dataset.Manifest
	ManifestError string
}
type Point struct {
	Date                 string     `json:"date"`
	Role                 string     `json:"role"`
	SnapshotID           string     `json:"snapshot_id"`
	SnapshotRowID        string     `json:"snapshot_row_id"`
	Generation           string     `json:"generation"`
	AsOf                 *time.Time `json:"as_of"`
	Book                 string     `json:"book"`
	AccountID            string     `json:"account_id"`
	Equity               *float64   `json:"equity"`
	NormalizedNAV        *float64   `json:"normalized_nav"`
	StateSHA256          string     `json:"state_sha256"`
	Reconciliation       string     `json:"reconciliation"`
	ValuationComplete    bool       `json:"valuation_complete"`
	CashFlowKnown        bool       `json:"cash_flow_known"`
	ExternalFlow         *float64   `json:"external_flow"`
	UnexplainedCashDelta *float64   `json:"unexplained_cash_delta"`
	Reasons              []string   `json:"reasons"`
}
type Report struct {
	Version            int       `json:"version"`
	Scope              string    `json:"scope"`
	AccountType        string    `json:"account_type"`
	AccountID          string    `json:"account_id"`
	Book               string    `json:"book"`
	Currency           string    `json:"currency"`
	Frequency          string    `json:"frequency"`
	StartDate          string    `json:"start_date"`
	EndDate            string    `json:"end_date"`
	AsOf               time.Time `json:"as_of"`
	InitialDate        string    `json:"initial_date"`
	InitialPointPolicy string    `json:"initial_point_policy"`
	ValuationPolicy    string    `json:"valuation_policy"`
	FeePolicy          string    `json:"fee_policy"`
	CashFlowPolicy     string    `json:"cash_flow_policy"`
	CashFlowBasis      string    `json:"cash_flow_basis"`
	CalendarVersion    string    `json:"calendar_version"`
	ExpectedDates      []string  `json:"expected_dates"`
	MissingDates       []string  `json:"missing_dates"`
	Points             []Point   `json:"points"`
	Reasons            []string  `json:"reasons"`
	LedgerHash         string    `json:"ledger_hash"`
	ManifestID         string    `json:"manifest_id"`
	ManifestHash       string    `json:"manifest_hash"`
	ResearchAsOf       time.Time `json:"research_as_of"`
	BenchmarkSHA256    string    `json:"benchmark_sha256"`
}
type Result struct {
	Report                 Report
	InputJSON, InputSHA256 string
	InputReady             bool
}

func hash(p []byte) string { s := sha256.Sum256(p); return hex.EncodeToString(s[:]) }
func hashValue(v any) string {
	p, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return hash(p)
}
func date(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, tradecal.Shanghai())
}
func closeStart(d time.Time) time.Time { return d.Add(15 * time.Hour) }

func Normalize(req Request, now time.Time, cal *tradecal.Calendar) (Query, error) {
	if req.Account != "" && req.Account != "SIM" {
		return Query{}, fmt.Errorf("%w: SIM/default only", ErrInvalid)
	}
	req.Account = "SIM"
	if req.Frequency == "" {
		req.Frequency = "1d"
	}
	if req.Frequency != "1d" || len(req.BenchmarkJSON) > 1<<20 {
		return Query{}, fmt.Errorf("%w: daily frequency/benchmark size", ErrInvalid)
	}
	if req.ManifestID != "" && !manifestIdentity.MatchString(req.ManifestID) {
		return Query{}, fmt.Errorf("%w: manifest content identity", ErrInvalid)
	}
	from, err := date(req.StartDate)
	if err != nil {
		return Query{}, fmt.Errorf("%w: start_date", ErrInvalid)
	}
	to, err := date(req.EndDate)
	if err != nil || to.Before(from) || to.Sub(from) > 366*24*time.Hour {
		return Query{}, fmt.Errorf("%w: end_date/range", ErrInvalid)
	}
	if req.AsOf.IsZero() {
		req.AsOf = now.UTC()
	}
	if req.AsOf.After(now) {
		return Query{}, fmt.Errorf("%w: future cutoff", ErrInvalid)
	}
	initial, err := cal.PrevOpen(from)
	if err != nil {
		return Query{}, err
	}
	q := Query{Request: req, InitialDate: initial.Format("2006-01-02"), From: closeStart(initial), To: closeStart(to).Add(10 * time.Minute), ExpectedDates: []string{initial.Format("2006-01-02")}}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		open, err := cal.Open(d)
		if err != nil {
			return Query{}, err
		}
		if open {
			q.ExpectedDates = append(q.ExpectedDates, d.Format("2006-01-02"))
		}
	}
	if len(q.ExpectedDates) < 2 {
		return Query{}, fmt.Errorf("%w: no requested trading day", ErrInvalid)
	}
	if q.To.After(req.AsOf) {
		q.To = req.AsOf
	}
	return q, nil
}

func closingValuation(s accountstate.Snapshot, day time.Time) bool {
	if len(s.Holdings) == 0 {
		return true
	}
	p := s.Valuation
	if p == nil || p.Version != 1 || !p.Complete || p.Source != "market.validated_minute_bar" || len(p.Marks) != len(s.Holdings) {
		return false
	}
	start := day.Add(14*time.Hour + 59*time.Minute)
	for _, m := range p.Marks {
		if !m.BarStart.Equal(start) || !m.BarEnd.Equal(start.Add(time.Minute)) {
			return false
		}
	}
	return true
}

func Build(q Query, e Evidence) Result {
	r := Report{Version: 1, Scope: "account_nav_export", AccountType: "SIM", AccountID: "default", Book: "paper", Currency: "CNY", Frequency: "1d", StartDate: q.StartDate, EndDate: q.EndDate, AsOf: q.AsOf, InitialDate: q.InitialDate,
		InitialPointPolicy: "actual_previous_trading_day_close; initial comparison point is not a fabricated deposit or return period",
		ValuationPolicy:    "latest actual full snapshot during [15:00,15:10); every holding must match its validated same-day 14:59 closed minute bar",
		FeePolicy:          "net_of_costs", CashFlowPolicy: "unknown", CashFlowBasis: "closed SIM ledger; adjacent cash changes must equal actual fill amounts minus fees; no broker deposit/withdrawal history is asserted",
		ExpectedDates: q.ExpectedDates, MissingDates: []string{}, Points: []Point{}, Reasons: []string{}, ResearchAsOf: q.AsOf}
	r.CalendarVersion = "sha256:" + hashValue(q.ExpectedDates)
	if len(q.ExpectedDates) < 2 {
		r.Reasons = append(r.Reasons, "normalized_daily_query_required")
		return Result{Report: r}
	}
	rows := map[string]SnapshotRow{}
	for _, row := range e.Snapshots {
		local := row.At.In(tradecal.Shanghai())
		key := local.Format("2006-01-02")
		if row.Book != "paper" || row.At.After(q.AsOf) || local.Hour() != 15 || local.Minute() >= 10 {
			continue
		}
		if old, ok := rows[key]; !ok || row.At.After(old.At) || row.At.Equal(old.At) && row.ID > old.ID {
			rows[key] = row
		}
	}
	states := map[string]accountstate.Snapshot{}
	seenSnapshots := map[string]bool{}
	for i, key := range q.ExpectedDates {
		p := Point{Date: key, Role: "closing", Book: "paper", AccountID: "default", Reconciliation: "unknown", Reasons: []string{}}
		if i == 0 {
			p.Role = "initial"
		}
		day, _ := date(key)
		if q.AsOf.Before(closeStart(day).Add(10 * time.Minute)) {
			p.Reasons = append(p.Reasons, "trading_day_not_closed")
		}
		row, ok := rows[key]
		if !ok {
			p.Reasons = append(p.Reasons, "actual_closing_snapshot_missing")
			r.MissingDates = append(r.MissingDates, key)
			r.Points = append(r.Points, p)
			continue
		}
		p.SnapshotRowID = strconv.Itoa(row.ID)
		p.StateSHA256 = hash(row.State)
		s, err := accountstate.Decode(row.State)
		precisionDelta := s.AsOf.Sub(row.At)
		if precisionDelta < 0 {
			precisionDelta = -precisionDelta
		}
		if err != nil || s.AccountType != risk.SIM || s.AccountID != "default" || s.Generation != int64(row.ID) || precisionDelta > time.Microsecond {
			p.Reasons = append(p.Reasons, "full_snapshot_identity_invalid_or_legacy")
			r.Points = append(r.Points, p)
			continue
		}
		p.SnapshotID = s.SnapshotID
		p.Generation = strconv.FormatInt(s.Generation, 10)
		at := s.AsOf
		p.AsOf = &at
		equity := s.Equity
		p.Equity = &equity
		p.Reconciliation = s.Reconciliation.Status
		if seenSnapshots[s.SnapshotID] {
			p.Reasons = append(p.Reasons, "snapshot_identity_reused")
		}
		seenSnapshots[s.SnapshotID] = true
		states[key] = s
		if s.Reconciliation.Status != "matched" || s.AsOf.Sub(s.Reconciliation.AsOf) > 60*time.Second {
			p.Reasons = append(p.Reasons, "reconciliation_unmatched_or_stale")
		}
		if !s.DayStartKnown {
			p.Reasons = append(p.Reasons, "day_start_unknown")
		}
		if s.Equity <= 0 {
			p.Reasons = append(p.Reasons, "nonpositive_equity")
		}
		p.ValuationComplete = closingValuation(s, day)
		if !p.ValuationComplete {
			p.Reasons = append(p.Reasons, "closing_valuation_provenance_unknown")
		}
		r.Points = append(r.Points, p)
	}
	flowsKnown := true
	usedFills := []Fill{}
	sort.Slice(e.Fills, func(i, j int) bool {
		if e.Fills[i].At.Equal(e.Fills[j].At) {
			return e.Fills[i].ID < e.Fills[j].ID
		}
		return e.Fills[i].At.Before(e.Fills[j].At)
	})
	for i := range r.Points {
		p := &r.Points[i]
		cur, ok := states[p.Date]
		if !ok {
			flowsKnown = false
			continue
		}
		if i == 0 {
			zero := 0.0
			p.ExternalFlow = &zero
			p.CashFlowKnown = true
			continue
		}
		prev, ok := states[r.Points[i-1].Date]
		if !ok || !cur.AsOf.After(prev.AsOf) || cur.Generation <= prev.Generation {
			p.Reasons = append(p.Reasons, "cash_flow_baseline_unknown")
			flowsKnown = false
			continue
		}
		explained := 0.0
		valid := true
		ids := map[string]bool{}
		fillIDs := map[int]bool{}
		for _, id := range cur.ObservedOrders {
			ids[id] = true
		}
		for _, fill := range e.Fills {
			if !fill.At.After(prev.AsOf) || fill.At.After(cur.AsOf) {
				continue
			}
			usedFills = append(usedFills, fill)
			if fill.ID <= 0 || fillIDs[fill.ID] || fill.Book != "paper" || !fill.OriginalOrderKnown || !ids[fill.ClientOrderID] || fill.Qty <= 0 || !finitePositive(fill.Price) || !finitePositive(fill.Amount) || fill.Fee < 0 || math.IsNaN(fill.Fee) || math.IsInf(fill.Fee, 0) || math.Abs(fill.Amount-fill.Price*float64(fill.Qty)) > 0.02 {
				valid = false
				continue
			}
			fillIDs[fill.ID] = true
			switch fill.Side {
			case "buy":
				explained -= fill.Amount + fill.Fee
			case "sell":
				explained += fill.Amount - fill.Fee
			default:
				valid = false
			}
		}
		delta := cur.Cash - prev.Cash - explained
		if !valid || math.Abs(delta) > 0.02 {
			p.UnexplainedCashDelta = &delta
			p.Reasons = append(p.Reasons, "cash_delta_not_explained_by_actual_fills")
			flowsKnown = false
			continue
		}
		zero := 0.0
		p.ExternalFlow = &zero
		p.CashFlowKnown = true
	}
	if flowsKnown {
		r.CashFlowPolicy = "no_external_flows"
	}
	if flowsKnown && len(r.Points[0].Reasons) == 0 && r.Points[0].Equity != nil && *r.Points[0].Equity > 0 {
		for i := range r.Points {
			p := &r.Points[i]
			if len(p.Reasons) == 0 && p.Equity != nil && p.ValuationComplete && p.CashFlowKnown {
				v := *p.Equity / *r.Points[0].Equity
				p.NormalizedNAV = &v
			}
		}
	}
	for _, p := range r.Points {
		for _, reason := range p.Reasons {
			r.Reasons = append(r.Reasons, p.Date+":"+reason)
		}
	}
	r.LedgerHash = hashValue(struct {
		Dates  []string
		Points []Point
		Fills  []Fill
	}{q.ExpectedDates, r.Points, usedFills})
	if e.Manifest == nil {
		r.Reasons = append(r.Reasons, "manifest_required_or_unavailable")
	} else {
		m := e.Manifest
		r.ManifestID, r.ManifestHash = m.ManifestID, m.Hash
		if err := m.Verify(); err != nil || m.ManifestID != q.ManifestID {
			r.Reasons = append(r.Reasons, "manifest_identity_invalid")
		} else {
			if m.From.In(tradecal.Shanghai()).Format("2006-01-02") > q.InitialDate || m.To.In(tradecal.Shanghai()).Format("2006-01-02") < q.ExpectedDates[len(q.ExpectedDates)-1] {
				r.Reasons = append(r.Reasons, "manifest_range_not_covering_nav")
			}
			if r.ResearchAsOf.After(m.Cutoff) {
				r.ResearchAsOf = m.Cutoff
			}
			known := map[string]bool{}
			for _, at := range m.Calendar {
				known[at.In(tradecal.Shanghai()).Format("2006-01-02")] = true
			}
			for _, key := range q.ExpectedDates {
				if !known[key] {
					r.Reasons = append(r.Reasons, "manifest_calendar_not_covering_nav")
					break
				}
			}
			for _, p := range r.Points {
				if p.AsOf != nil && p.AsOf.After(r.ResearchAsOf) {
					r.Reasons = append(r.Reasons, "manifest_cutoff_precedes_nav_observation")
					break
				}
			}
		}
	}
	if e.ManifestError != "" {
		r.Reasons = append(r.Reasons, e.ManifestError)
	}
	benchmark, err := parseBenchmark(q.BenchmarkJSON, q.ExpectedDates)
	if err != nil {
		r.Reasons = append(r.Reasons, err.Error())
	} else {
		r.BenchmarkSHA256 = hash([]byte(q.BenchmarkJSON))
	}
	result := Result{Report: r}
	if len(r.Reasons) != 0 || !flowsKnown {
		return result
	}
	points := make([]workerPoint, len(r.Points))
	for i, p := range r.Points {
		points[i] = workerPoint{Date: p.Date, NAV: *p.Equity, Benchmark: benchmark.Points[i].Value, ExternalFlow: 0, ValuationComplete: true, SnapshotID: p.SnapshotID, Generation: p.Generation, AsOf: *p.AsOf, StateSHA256: p.StateSHA256}
	}
	input := workerInput{Kind: "account_nav", FeePolicy: "net_of_costs", Frequency: "1d", CashFlowPolicy: "no_external_flows", Currency: "CNY", LedgerHash: r.LedgerHash, CalendarVersion: r.CalendarVersion, ManifestID: r.ManifestID, ManifestHash: r.ManifestHash, BenchmarkID: benchmark.ID, BenchmarkKind: benchmark.Kind, ExpectedDates: q.ExpectedDates, Points: points}
	encoded, _ := json.Marshal(input)
	result.InputJSON = string(encoded)
	result.InputSHA256 = hash(encoded)
	result.InputReady = true
	return result
}

func finitePositive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

type benchmarkPoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}
type benchmark struct {
	ID     string           `json:"benchmark_id"`
	Kind   string           `json:"benchmark_kind"`
	Points []benchmarkPoint `json:"points"`
}

func parseBenchmark(raw string, dates []string) (benchmark, error) {
	var b benchmark
	if raw == "" {
		return b, errors.New("explicit_aligned_benchmark_required")
	}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&b); err != nil {
		return b, errors.New("benchmark_contract_invalid")
	}
	var rest any
	if d.Decode(&rest) != io.EOF {
		return b, errors.New("benchmark_contract_invalid")
	}
	if b.ID == "" || len(b.ID) > 128 || b.Kind != "price_index" && b.Kind != "total_return_index" || len(b.Points) != len(dates) {
		return b, errors.New("benchmark_dates_not_aligned")
	}
	for i, p := range b.Points {
		if p.Date != dates[i] || !finitePositive(p.Value) {
			return b, errors.New("benchmark_dates_not_aligned")
		}
	}
	return b, nil
}

type workerPoint struct {
	Date              string    `json:"date"`
	NAV               float64   `json:"nav"`
	Benchmark         float64   `json:"benchmark"`
	ExternalFlow      float64   `json:"external_flow"`
	ValuationComplete bool      `json:"valuation_complete"`
	SnapshotID        string    `json:"snapshot_id"`
	Generation        string    `json:"generation"`
	AsOf              time.Time `json:"as_of"`
	StateSHA256       string    `json:"state_sha256"`
}
type workerInput struct {
	Kind            string        `json:"kind"`
	FeePolicy       string        `json:"fee_policy"`
	Frequency       string        `json:"frequency"`
	CashFlowPolicy  string        `json:"cash_flow_policy"`
	Currency        string        `json:"currency"`
	LedgerHash      string        `json:"ledger_hash"`
	CalendarVersion string        `json:"calendar_version"`
	ManifestID      string        `json:"manifest_id"`
	ManifestHash    string        `json:"manifest_hash"`
	BenchmarkID     string        `json:"benchmark_id"`
	BenchmarkKind   string        `json:"benchmark_kind"`
	ExpectedDates   []string      `json:"expected_dates"`
	Points          []workerPoint `json:"points"`
}
