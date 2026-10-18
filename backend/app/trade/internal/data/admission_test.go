package data

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"server/conf"
	"server/ent/paperrunsample"
	"server/pkg/admission"
	"server/pkg/tradecal"
	"sync"
)

func TestAdmissionRiskReadinessMustBeExplicit(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ready      *bool
	}{
		{"legacy_unknown", `{"mode":"L1","configReady":true,"configRevision":1}`, nil},
		{"engine_not_ready", `{"mode":"L1","configReady":true,"configRevision":1,"ready":false}`, boolPtr(false)},
		{"engine_ready", `{"mode":"L1","configReady":true,"configRevision":1,"ready":true}`, boolPtr(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/risk/v1/state" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := NewRiskClient(&conf.Trade{RiskAddr: srv.URL})
			s, err := c.ReadAdmissionRisk(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if (s.RiskReady == nil) != (tc.ready == nil) || tc.ready != nil && *s.RiskReady != *tc.ready {
				t.Fatalf("risk ready unknown was replaced: %+v", s)
			}
			if s.KillActive != nil || s.RiskAccountAsOf != nil {
				t.Fatal("missing kill/account data must remain unknown")
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }

func TestAdmissionClosingSourceObservation(t *testing.T) {
	local := tradecal.Shanghai()
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, local) }
	for _, tc := range []struct {
		name          string
		success, now  time.Time
		closing, want bool
	}{
		{"intraday_fresh", at(16, 10, 0), at(16, 10, 1), false, true},
		{"intraday_stale", at(16, 10, 0), at(16, 10, 3), false, false},
		{"close_collection_stopped", at(16, 14, 59), at(16, 15, 9), true, true},
		{"before_close_cutoff", at(16, 14, 54), at(16, 15, 9), true, false},
		{"prior_day", at(15, 14, 59), at(16, 15, 9), true, false},
		{"future_source", at(16, 15, 10), at(16, 15, 9), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := admissionSourceObserved(tc.success, tc.now, tc.closing); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestAdmissionRejectsHistoricalAndFutureWritesBeforeDatabaseAccess(t *testing.T) {
	r := NewRepo(nil, log.DefaultLogger)
	for _, delta := range []time.Duration{-time.Minute, 3 * time.Second} {
		at := time.Now().Add(delta)
		s := admission.Sample{Version: admission.Version, Book: "paper", AccountID: "default", InstanceID: "synthetic-write-guard", ClockSource: "server_wall_clock", ObservedAt: at, EndedAt: at}
		s.Seal()
		if err := r.SaveAdmissionSample(context.Background(), s); err == nil {
			t.Fatal("backfill/future write accepted")
		}
	}
}

func TestPostgresAdmissionAppendOnlySlotAndStoredIdentity(t *testing.T) {
	c := accountTestClient(t)
	ctx := context.Background()
	r := NewRepo(c, log.DefaultLogger)
	instance := "synthetic-db-" + uuid.NewString()
	t.Cleanup(func() { _, _ = c.PaperRunSample.Delete().Where(paperrunsample.InstanceIDEQ(instance)).Exec(ctx) })
	at := time.Now()
	s := admission.Sample{Version: admission.Version, Book: "paper", AccountID: "default", InstanceID: instance, ClockSource: "server_wall_clock", ObservedAt: at, EndedAt: at, AccountSnapshotID: "synthetic-original"}
	s.Seal()
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.SaveAdmissionSample(ctx, s); err != nil {
				t.Errorf("slot insert: %v", err)
			}
		}()
	}
	wg.Wait()
	changed := s
	changed.AccountSnapshotID = "synthetic-changed"
	changed.Seal()
	if err := r.SaveAdmissionSample(ctx, changed); err != nil {
		t.Fatal(err)
	}
	rows, err := c.PaperRunSample.Query().Where(paperrunsample.InstanceIDEQ(instance)).All(ctx)
	if err != nil || len(rows) != 1 || rows[0].EvidenceHash != s.EvidenceHash {
		t.Fatalf("slot overwritten or duplicated: rows=%v err=%v", rows, err)
	}
	read, err := r.ReadAdmissionSamples(ctx, at.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range read {
		if row.InstanceID == instance {
			found = true
			if row.AccountSnapshotID != s.AccountSnapshotID {
				t.Fatal("immutable observation replaced")
			}
		}
	}
	if !found {
		t.Fatal("persisted sample missing")
	}
	// This deliberately corrupt row is confined to the isolated test database;
	// the reader must fail rather than silently discard an unverifiable sample.
	_, err = c.PaperRunSample.Create().SetBook("paper").SetAccountID("default").SetTradingDay(s.TradingDay).SetSlotStart(s.SlotStart.Add(admission.SlotWidth)).SetInstanceID(instance).SetObservedAt(s.ObservedAt).SetObservation(rows[0].Observation).SetEvidenceHash("invalid").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadAdmissionSamples(ctx, at.Add(-time.Second)); err == nil {
		t.Fatal("stored identity corruption ignored")
	}
}
