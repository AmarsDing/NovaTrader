package biz

import (
	"context"
	"strings"
	"testing"
	"time"

	"server/pkg/admission"
	"server/pkg/tradecal"
)

// syntheticAdmissionStore exists only in tests. Production repositories reject
// historical writes; these fixtures exercise the state machine, not admission.
type syntheticAdmissionStore struct {
	*MemStore
	samples []admission.Sample
}

func (*syntheticAdmissionStore) AdmissionSlotExists(context.Context, time.Time) (bool, error) {
	return false, nil
}
func (*syntheticAdmissionStore) CollectAdmissionFacts(context.Context, time.Time) (AdmissionFacts, error) {
	panic("test fixture has no production collector")
}
func (*syntheticAdmissionStore) SaveAdmissionSample(context.Context, admission.Sample) error {
	panic("test fixtures must never be persisted")
}
func (s *syntheticAdmissionStore) ReadAdmissionSamples(context.Context, time.Time) ([]admission.Sample, error) {
	return s.samples, nil
}

func withSyntheticAdmission(t *testing.T, e *Engine, mem *MemStore) {
	t.Helper()
	pointer := func(v bool) *bool { return &v }
	integer := func(v int) *int { return &v }
	now := time.Now().In(tradecal.Shanghai())
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tradecal.Shanghai())
	open, err := tradecal.Default.Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if !open || now.Before(d.Add(15*time.Hour+10*time.Minute)) {
		d, err = tradecal.Default.PrevOpen(d)
		if err != nil {
			t.Fatal(err)
		}
	}
	store := &syntheticAdmissionStore{MemStore: mem}
	add := func(at time.Time) {
		s := admission.Sample{
			Version: admission.Version, Book: "paper", AccountID: "default", InstanceID: "synthetic-state-machine-only", ClockSource: "server_wall_clock",
			ObservedAt: at, EndedAt: at.Add(time.Millisecond), AccountSnapshotID: "synthetic-snapshot", AccountGeneration: 1, AccountAsOf: &at,
			AccountTradingDay: at.Format("2006-01-02"), DayStartKnown: pointer(true), LedgerMatched: pointer(true), Reconciliation: "matched", ReconciliationAsOf: &at,
			RiskReady: pointer(true), RiskMode: "L1", KillActive: pointer(false), ConfigReady: pointer(true), RiskConfigRevision: integer(1), StoredConfigRevision: integer(1), RiskAccountAsOf: &at,
			UnresolvedIntents: integer(0), UnknownOrders: integer(0), OutboxKnown: true, OutboxDead: integer(0),
			UniverseKnown: true, UniverseCount: integer(5000), UniverseBasis: "synthetic-only", MarketRows: integer(5000), FreshRows: integer(5000), ClosingRows: integer(5000),
			MarketOldestSourceTime: &at, MarketSources: map[string]int{"synthetic": 5000}, MarketSourceHealthy: pointer(true), CollectionErrors: []string{},
		}
		s.Seal()
		store.samples = append(store.samples, s)
	}
	for range admission.MinRealTradingDays {
		for at := d; at.Before(d.AddDate(0, 0, 1)); at = at.Add(admission.SlotWidth) {
			session, err := tradecal.Default.SessionAt(at)
			if err != nil {
				t.Fatal(err)
			}
			if session.Trading {
				add(at.Add(time.Second))
			}
		}
		add(d.Add(15*time.Hour + 9*time.Minute + 31*time.Second))
		d, err = tradecal.Default.PrevOpen(d)
		if err != nil {
			t.Fatal(err)
		}
	}
	e.store = store
}

func TestOldPaperDaysCannotSatisfyOperationEvidence(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.set.LiveEnabled, e.set.BrokerConnected = true, true // Isolated state-machine fixture only.
	if err := mem.Commit(context.Background(), Batch{Cash: &Cash{Book: BookPaper, Cash: 1_000_000, PaperDays: 999}}); err != nil {
		t.Fatal(err)
	}
	o, err := e.Place(context.Background(), PlaceRequest{ClientOrderID: "old-days", Account: "LIVE", Symbol: "600519.SH", Side: "buy", Price: 10, Volume: 100})
	if err != nil || o.Status != StatusRejected || !strings.Contains(o.Reason, "旧paper_days") {
		t.Fatalf("legacy counter bypassed actual evidence: %+v err=%v", o, err)
	}
}

func TestSyntheticAdmissionCannotOpenDisconnectedBroker(t *testing.T) {
	e, mem := newEngine(t, allow{})
	withSyntheticAdmission(t, e, mem)
	e.set.LiveEnabled = true
	r, err := e.PaperAdmission(context.Background(), "SIM", 21)
	if err != nil || !r.RunEvidenceReady || r.LiveEnabled {
		t.Fatalf("synthetic fixture failed: %+v err=%v", r, err)
	}
	o, err := e.Place(context.Background(), PlaceRequest{ClientOrderID: "no-broker", Account: "LIVE", Symbol: "600519.SH", Side: "buy", Price: 10, Volume: 100})
	if err != nil || o.Status != StatusRejected || !strings.Contains(o.Reason, "柜台通道") {
		t.Fatalf("operation evidence bypassed broker connection: %+v err=%v", o, err)
	}
}
