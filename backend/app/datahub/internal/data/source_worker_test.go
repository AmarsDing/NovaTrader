package data

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"server/app/datahub/internal/biz"
	"server/conf"

	"github.com/go-kratos/kratos/v2/log"
)

func TestSupplementDefaultDisabledAndNeverSnapshot(t *testing.T) {
	sources, err := (sourceOpts{cfg: &conf.Datahub{}, log: log.NewHelper(log.DefaultLogger)}).build()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Name() == biz.SourceTdxArchive || source.Name() == biz.SourceOpenBBFred {
			t.Fatal("supplement enabled by default")
		}
	}
	tdx, err := NewTdxArchiveSource("http://127.0.0.1:18082")
	if err != nil {
		t.Fatal(err)
	}
	macro, err := NewOpenBBFredSource("http://127.0.0.1:18082")
	if err != nil {
		t.Fatal(err)
	}
	if !biz.Supports(biz.DomainDailyBar, tdx) || biz.Supports(biz.DomainSnapshot, tdx) || biz.Supports(biz.DomainSecurity, tdx) || biz.Supports(biz.DomainMacro, tdx) || !biz.Supports(biz.DomainMacro, macro) || biz.Supports(biz.DomainSnapshot, macro) {
		t.Fatal("optional source expanded its domain")
	}
	p, warnings := biz.NewPipeline(biz.Config{Sources: map[string]biz.SourceConfig{tdx.Name(): {Enabled: true, Official: true}}, Domains: map[string]biz.DomainConfig{biz.DomainDailyBar: {Enabled: true, Sources: []string{tdx.Name()}}, biz.DomainSnapshot: {Enabled: true, Sources: []string{tdx.Name()}}}}, []biz.Source{tdx}, nil)
	if len(warnings) != 1 || p.DomainEnabled(biz.DomainSnapshot) || len(p.Health()) != 1 || p.Health()[0].State != "coverage_unknown" {
		t.Fatal("unknown coverage became healthy snapshot", p.Health(), warnings)
	}
}

func TestSourceWorkerRejectsFreeDestinationsAndRedirects(t *testing.T) {
	for _, base := range []string{"https://example.org", "http://localhost:18082", "http://127.0.0.1", "http://secret@127.0.0.1:18082", "http://127.0.0.1:18082/private", "http://127.0.0.1:18082?key=secret", "file:///secret"} {
		if _, err := NewTdxArchiveSource(base); err == nil {
			t.Fatalf("free destination accepted: %s", base)
		}
	}
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	s, _ := NewTdxArchiveSource(redirect.URL)
	_, err := s.DailyBars(context.Background(), archiveQuery())
	if err == nil || reached {
		t.Fatal("source followed redirect")
	}
}

func archiveQuery() biz.BarQuery {
	d := time.Date(2025, 1, 6, 0, 0, 0, 0, shanghaiLoc())
	return biz.BarQuery{Freq: "1d", Start: d, End: d, Expect: d}
}
func syntheticSourceReply() map[string]any {
	rows := []map[string]any{}
	for _, sym := range []string{"600001.SH", "000001.SZ", "920001.BJ"} {
		rows = append(rows, map[string]any{"symbol": sym, "name": "合成合同样本", "time": "2025-01-06T00:00:00+08:00", "freq": "1d", "open": 10, "high": 11, "low": 9, "close": 10.5, "pre_close": 10, "volume": 1000, "amount": 10500, "available_at": nil, "availability": "unknown", "availability_evidence": "synthetic test fixture"})
	}
	raw, _ := json.Marshal(rows)
	return map[string]any{"schema_version": 1, "status": "succeeded", "domain": "daily_bar", "provider": "tdx.com.cn", "provider_raw_sha256": sourceHash([]byte("explicit synthetic source fixture")), "provider_raw_kind": "provider_http_zip_bytes", "acquisition_mode": "recorded_provider_bytes", "records_sha256": sourceHash(raw), "record_count": len(rows), "records": json.RawMessage(raw), "observed_at": time.Now().UTC().Format(time.RFC3339Nano), "available_at": nil, "training_ready": false, "metadata": map[string]any{}, "coverage": map[string]any{"coverage_ratio": nil, "markets": map[string]any{"sh": map[string]any{"status": "observed", "a_stock_count": 1}, "sz": map[string]any{"status": "observed", "a_stock_count": 1}, "bj": map[string]any{"status": "observed", "a_stock_count": 1}}}}
}

func TestSourceRecordsValidateHashClockCoverageAndOHLC(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"valid", func(map[string]any) {}},
		{"wrong_hash", func(r map[string]any) { r["records_sha256"] = strings.Repeat("0", 64) }},
		{"false_publication", func(r map[string]any) { r["available_at"] = time.Now().UTC() }},
		{"false_training", func(r map[string]any) { r["training_ready"] = true }},
		{"future_observation", func(r map[string]any) { r["observed_at"] = time.Now().Add(time.Minute).UTC() }},
		{"old_observation", func(r map[string]any) { r["observed_at"] = time.Now().Add(-time.Hour).UTC() }},
		{"sample_masquerade", func(r map[string]any) { r["record_count"] = 5370 }},
		{"missing_bj", func(r map[string]any) {
			r["coverage"] = map[string]any{"coverage_ratio": nil, "markets": map[string]any{}}
		}},
		{"bad_ohlc", func(r map[string]any) {
			var rows []map[string]any
			_ = json.Unmarshal(r["records"].(json.RawMessage), &rows)
			rows[0]["open"] = 0
			raw, _ := json.Marshal(rows)
			r["records"], r["records_sha256"] = json.RawMessage(raw), sourceHash(raw)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := syntheticSourceReply()
			tc.mutate(reply)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/source/collect" {
					t.Error("wrong fixed path")
				}
				_ = json.NewEncoder(w).Encode(reply)
			}))
			defer server.Close()
			source, _ := NewTdxArchiveSource(server.URL)
			batch, err := source.DailyBars(context.Background(), archiveQuery())
			if tc.name != "valid" {
				if err == nil {
					t.Fatal("invalid provider record accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(batch.Bars) != 3 || !batch.CoverageUnknown || batch.Bars[0].AvailableAt != nil || batch.Bars[0].ObservedAt == nil || batch.Bars[0].Provenance == nil {
				t.Fatal("publication/provenance lost")
			}
		})
	}
}

func TestSourceWorkerUnavailableDoesNotLeakBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"reason":"secret-key-must-not-leak"}`))
	}))
	defer server.Close()
	source, _ := NewOpenBBFredSource(server.URL)
	_, err := source.Macro(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatal("unavailable provider body leaked", err)
	}
}
