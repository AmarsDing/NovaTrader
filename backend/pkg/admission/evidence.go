// Package admission evaluates evidence of actual SIM operation. It never enables LIVE or grants authority.
package admission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"server/pkg/tradecal"
)

const Version = 1
const SlotWidth = 30 * time.Second
const MinRealTradingDays = 20

type Sample struct {
	Version                int            `json:"version"`
	Book                   string         `json:"book"`
	AccountID              string         `json:"account_id"`
	TradingDay             string         `json:"trading_day"`
	InstanceID             string         `json:"instance_id"`
	ClockSource            string         `json:"clock_source"`
	SlotStart              time.Time      `json:"slot_start"`
	ObservedAt             time.Time      `json:"observed_at"`
	EndedAt                time.Time      `json:"ended_at"`
	AccountSnapshotID      string         `json:"account_snapshot_id"`
	AccountGeneration      int64          `json:"account_generation"`
	AccountAsOf            *time.Time     `json:"account_as_of"`
	AccountTradingDay      string         `json:"account_trading_day"`
	DayStartKnown          *bool          `json:"day_start_known"`
	LedgerMatched          *bool          `json:"ledger_matched"`
	Reconciliation         string         `json:"reconciliation"`
	ReconciliationAsOf     *time.Time     `json:"reconciliation_as_of"`
	RiskReady              *bool          `json:"risk_ready"`
	RiskMode               string         `json:"risk_mode"`
	KillActive             *bool          `json:"kill_active"`
	ConfigReady            *bool          `json:"config_ready"`
	RiskConfigRevision     *int           `json:"risk_config_revision"`
	StoredConfigRevision   *int           `json:"stored_config_revision"`
	RiskAccountAsOf        *time.Time     `json:"risk_account_as_of"`
	UnresolvedIntents      *int           `json:"unresolved_intents"`
	UnknownOrders          *int           `json:"unknown_orders"`
	OutboxDead             *int           `json:"outbox_dead"`
	OutboxOldest           *time.Time     `json:"outbox_oldest"`
	OutboxKnown            bool           `json:"outbox_known"`
	UniverseCount          *int           `json:"universe_count"`
	UniverseBasis          string         `json:"universe_basis"`
	UniverseKnown          bool           `json:"universe_known"`
	MarketRows             *int           `json:"market_rows"`
	FreshRows              *int           `json:"fresh_rows"`
	ClosingRows            *int           `json:"closing_rows"`
	MarketOldestSourceTime *time.Time     `json:"market_oldest_source_time"`
	MarketSources          map[string]int `json:"market_sources"`
	MarketSourceHealthy    *bool          `json:"market_source_healthy"`
	CollectionErrors       []string       `json:"collection_errors"`
	EvidenceHash           string         `json:"evidence_hash"`
}

func (s Sample) hash() string {
	s.EvidenceHash = ""
	p, e := json.Marshal(s)
	if e != nil {
		return ""
	}
	h := sha256.Sum256(p)
	return hex.EncodeToString(h[:])
}
func (s *Sample) Seal() {
	s.ObservedAt = s.ObservedAt.UTC().Truncate(time.Microsecond)
	s.EndedAt = s.EndedAt.UTC().Truncate(time.Microsecond)
	s.SlotStart = s.ObservedAt.Truncate(SlotWidth)
	s.TradingDay = s.ObservedAt.In(tradecal.Shanghai()).Format("2006-01-02")
	s.EvidenceHash = s.hash()
}
func (s Sample) Verify() error {
	if s.Version != Version || s.Book != "paper" || s.AccountID != "default" || s.InstanceID == "" || s.ClockSource != "server_wall_clock" || s.ObservedAt.IsZero() || s.EndedAt.Before(s.ObservedAt) || !s.SlotStart.Equal(s.ObservedAt.Truncate(SlotWidth)) || s.TradingDay != s.ObservedAt.In(tradecal.Shanghai()).Format("2006-01-02") || s.EvidenceHash == "" || s.hash() != s.EvidenceHash {
		return fmt.Errorf("admission: invalid or non-server-clock evidence")
	}
	return nil
}
func knownTrue(v *bool) bool { return v != nil && *v }
func knownZero(v *int) bool  { return v != nil && *v == 0 }
func fresh(at *time.Time, now time.Time, age time.Duration) bool {
	return at != nil && !at.IsZero() && !at.After(now.Add(time.Second)) && now.Sub(*at) <= age
}

// Problems retains unknown rather than converting it to zero. The session decision is owned by tradecal.
func (s Sample) Problems(trading bool) []string {
	out := []string{}
	add := func(ok bool, why string) {
		if !ok {
			out = append(out, why)
		}
	}
	add(s.Verify() == nil, "invalid_evidence_identity")
	add(s.EndedAt.Sub(s.ObservedAt) <= 3*time.Second, "collection_too_slow")
	add(s.AccountSnapshotID != "" && s.AccountGeneration > 0 && fresh(s.AccountAsOf, s.ObservedAt, 60*time.Second), "account_unknown_or_stale")
	add(s.AccountTradingDay == s.TradingDay && knownTrue(s.DayStartKnown), "account_day_start_unknown")
	add(knownTrue(s.LedgerMatched) && s.Reconciliation == "matched" && fresh(s.ReconciliationAsOf, s.ObservedAt, 60*time.Second), "ledger_or_reconciliation_unmatched")
	add(knownTrue(s.RiskReady) && fresh(s.RiskAccountAsOf, s.ObservedAt, 60*time.Second), "risk_unknown_or_stale")
	add(s.KillActive != nil && !*s.KillActive, "killswitch_unknown_or_active")
	add(knownTrue(s.ConfigReady) && s.RiskConfigRevision != nil && s.StoredConfigRevision != nil && *s.RiskConfigRevision == *s.StoredConfigRevision, "configuration_not_loaded")
	if trading {
		add(s.RiskMode == "L1", "sim_mode_not_active")
	}
	add(knownZero(s.UnresolvedIntents) && knownZero(s.UnknownOrders), "unresolved_execution_identity")
	add(s.OutboxKnown && knownZero(s.OutboxDead) && (s.OutboxOldest == nil || fresh(s.OutboxOldest, s.ObservedAt, 30*time.Second)), "outbox_unavailable_or_backlogged")
	add(s.UniverseKnown && s.UniverseCount != nil && *s.UniverseCount >= 3000 && s.UniverseBasis != "", "market_universe_unknown")
	if s.UniverseCount != nil && *s.UniverseCount > 0 {
		needed := int(math.Ceil(float64(*s.UniverseCount) * 0.95))
		add(s.MarketRows != nil && *s.MarketRows >= needed, "market_coverage_insufficient")
		if trading {
			add(s.FreshRows != nil && *s.FreshRows >= needed && fresh(s.MarketOldestSourceTime, s.ObservedAt, 10*time.Second), "market_source_time_stale")
		} else {
			add(s.ClosingRows != nil && *s.ClosingRows >= needed, "closing_market_evidence_missing")
		}
	}
	sourceRows := 0
	for source, count := range s.MarketSources {
		if source == "" || count <= 0 {
			sourceRows = -1
			break
		}
		sourceRows += count
	}
	add(knownTrue(s.MarketSourceHealthy) && sourceRows > 0 && s.MarketRows != nil && sourceRows == *s.MarketRows, "market_source_unknown")
	add(len(s.CollectionErrors) == 0, "collection_dependency_error")
	return out
}

type Day struct {
	TradingDay      string         `json:"trading_day"`
	Status          string         `json:"status"`
	RequiredSlots   int            `json:"required_slots"`
	ObservedSlots   int            `json:"observed_slots"`
	HealthySlots    int            `json:"healthy_slots"`
	MissingSlots    int            `json:"missing_slots"`
	UnhealthySlots  int            `json:"unhealthy_slots"`
	Samples         int            `json:"samples"`
	ClosingProof    bool           `json:"closing_proof"`
	FirstObservedAt *time.Time     `json:"first_observed_at"`
	LastObservedAt  *time.Time     `json:"last_observed_at"`
	Reasons         map[string]int `json:"reasons"`
}

type Report struct {
	Version                int       `json:"version"`
	AccountType            string    `json:"account_type"`
	AccountID              string    `json:"account_id"`
	EvaluatedAt            time.Time `json:"evaluated_at"`
	RequiredDays           int       `json:"required_days"`
	ConsecutiveDays        int       `json:"consecutive_days"`
	LatestClosedTradingDay string    `json:"latest_closed_trading_day"`
	RunEvidenceReady       bool      `json:"run_evidence_ready"`
	LiveEnabled            bool      `json:"live_enabled"`
	Days                   []Day     `json:"days"`
	Reasons                []string  `json:"reasons"`
	OtherRequiredEvidence  []string  `json:"other_required_evidence"`
	Policy                 string    `json:"policy"`
}

func closingWindow(at time.Time) bool {
	local := at.In(tradecal.Shanghai())
	return local.Hour() == 15 && local.Minute() < 10
}
func closedAt(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 15, 10, 0, 0, tradecal.Shanghai())
}

func evaluateDay(cal *tradecal.Calendar, d, now time.Time, input []Sample) (Day, error) {
	key := d.Format("2006-01-02")
	out := Day{TradingDay: key, Status: "incomplete", Reasons: map[string]int{}}
	required := map[int64]bool{}
	healthy := map[int64]bool{}
	observed := map[int64]bool{}
	closingObserved, closingHealthy := false, true
	for at := d; at.Before(d.AddDate(0, 0, 1)); at = at.Add(SlotWidth) {
		session, e := cal.SessionAt(at)
		if e != nil {
			return out, e
		}
		if session.Trading {
			required[at.Unix()] = true
		}
	}
	for _, s := range input {
		if s.TradingDay != key {
			continue
		}
		out.Samples++
		at := s.ObservedAt
		if out.FirstObservedAt == nil || at.Before(*out.FirstObservedAt) {
			v := at
			out.FirstObservedAt = &v
		}
		if out.LastObservedAt == nil || at.After(*out.LastObservedAt) {
			v := at
			out.LastObservedAt = &v
		}
		if s.ObservedAt.After(now.Add(time.Second)) || s.EndedAt.After(now.Add(time.Second)) {
			out.Reasons["future_evidence"]++
			continue
		}
		key := s.SlotStart.Unix()
		if required[key] {
			problems := s.Problems(true)
			if !observed[key] {
				healthy[key] = true
			}
			observed[key] = true
			if len(problems) > 0 {
				healthy[key] = false
				for _, p := range problems {
					out.Reasons[p]++
				}
			}
		}
		if closingWindow(s.ObservedAt) && s.ObservedAt.In(tradecal.Shanghai()).Minute() == 9 && s.ObservedAt.In(tradecal.Shanghai()).Second() >= 30 {
			closingObserved = true
			problems := s.Problems(false)
			if len(problems) > 0 {
				closingHealthy = false
				for _, p := range problems {
					out.Reasons[p]++
				}
			}
		}
	}
	// Every instance observed in the final close-proof slot must be healthy.
	// A good replica cannot hide another replica's failed ledger or risk state.
	out.ClosingProof = closingObserved && closingHealthy
	out.RequiredSlots = len(required)
	out.ObservedSlots = len(observed)
	for k := range observed {
		if healthy[k] {
			out.HealthySlots++
		} else {
			out.UnhealthySlots++
		}
	}
	out.MissingSlots = out.RequiredSlots - out.ObservedSlots
	if out.MissingSlots > 0 {
		out.Reasons["missing_session_slots"] = out.MissingSlots
	}
	if !out.ClosingProof {
		out.Reasons["closing_proof_missing"]++
	}
	if now.Before(closedAt(d)) {
		out.Status = "running"
		out.Reasons["trading_day_not_final"]++
	} else if out.MissingSlots == 0 && out.UnhealthySlots == 0 && out.ClosingProof && len(out.Reasons) == 0 {
		out.Status = "qualified"
	}
	return out, nil
}

// Evaluate anchors continuity to the latest *closed actual trading day*, not the latest available sample.
// Synthetic fixtures may exercise this pure function; only the server collector can persist production evidence.
func Evaluate(cal *tradecal.Calendar, now time.Time, samples []Sample, requiredDays, limit int) (Report, error) {
	if requiredDays < MinRealTradingDays {
		requiredDays = MinRealTradingDays
	}
	if limit < requiredDays+1 {
		limit = requiredDays + 1
	}
	if limit > 120 {
		limit = 120
	}
	out := Report{Version: Version, AccountType: "SIM", AccountID: "default", EvaluatedAt: now, RequiredDays: requiredDays, LiveEnabled: false, Days: []Day{}, Reasons: []string{}, OtherRequiredEvidence: []string{"风险故障演练通过", "真实文件单、回报与账户对账验收", "账户/策略/预算的独立用户授权"}, Policy: "server-clock-v1: 30s slots, all AM/PM slots healthy, >=95% current validated universe, close proof by15:10; no RollDay credits"}
	today := time.Date(now.In(tradecal.Shanghai()).Year(), now.In(tradecal.Shanghai()).Month(), now.In(tradecal.Shanghai()).Day(), 0, 0, 0, 0, tradecal.Shanghai())
	open, e := cal.Open(today)
	if e != nil {
		return out, e
	}
	anchor := today
	if !open || now.Before(closedAt(today)) {
		anchor, e = cal.PrevOpen(today)
		if e != nil {
			return out, e
		}
	}
	out.LatestClosedTradingDay = anchor.Format("2006-01-02")
	grouped := map[string][]Sample{}
	for _, s := range samples {
		grouped[s.TradingDay] = append(grouped[s.TradingDay], s)
	}
	d := anchor
	continuous := true
	for i := 0; i < limit; i++ {
		day, e := evaluateDay(cal, d, now, grouped[d.Format("2006-01-02")])
		if e != nil {
			return out, e
		}
		out.Days = append(out.Days, day)
		if continuous && day.Status == "qualified" {
			out.ConsecutiveDays++
		} else {
			continuous = false
		}
		d, e = cal.PrevOpen(d)
		if e != nil {
			break
		}
	}
	if open && now.Before(closedAt(today)) {
		day, e := evaluateDay(cal, today, now, grouped[today.Format("2006-01-02")])
		if e != nil {
			return out, e
		}
		out.Days = append(out.Days, day)
	}
	sort.Slice(out.Days, func(i, j int) bool { return out.Days[i].TradingDay > out.Days[j].TradingDay })
	out.RunEvidenceReady = out.ConsecutiveDays >= requiredDays
	if !out.RunEvidenceReady {
		out.Reasons = append(out.Reasons, fmt.Sprintf("连续合格运行%d/%d个真实交易日", out.ConsecutiveDays, requiredDays))
	}
	return out, nil
}
