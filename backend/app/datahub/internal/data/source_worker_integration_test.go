package data

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"server/app/datahub/internal/biz"
	"server/ent"
	"server/ent/datasetrecord"
	"server/ent/marketdata"
	"server/ent/outbox"
	"server/pkg/dataset"
	"server/pkg/dbinit"
	"server/pkg/entclient"
)

func sourceAdapterPG(t *testing.T) *ent.Client {
	t.Helper()
	raw := os.Getenv("NT_TEST_PG_PORT")
	if raw == "" {
		t.Skip("set isolated NT_TEST_PG_PORT")
	}
	port, e := strconv.Atoi(raw)
	if e != nil || port == 5432 || port < 1 || port > 65535 {
		t.Fatal("requires non-production isolated PG port")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, e := entclient.Open(ctx, entclient.Config{DB: dbinit.Config{Host: "127.0.0.1", Port: int32(port), User: "postgres", Password: "postgres", Database: "novatrader_source_adapter_test", SSLMode: "disable", Timeout: 3 * time.Second}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	return c.Client
}

func sourceHTTPProcess(t *testing.T) string {
	t.Helper()
	python := os.Getenv("NT_TEST_SOURCE_PYTHON")
	if python == "" {
		t.Skip("set NT_TEST_SOURCE_PYTHON for actual isolated source process")
	}
	root, e := filepath.Abs("../../../../..")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, python, filepath.Join(root, "source-worker", "http_worker.py"), "--enabled", "--recorded-only", "--port", "0", "--evidence-dir", t.TempDir())
	cmd.Env = []string{"PYTHONUTF8=1", "PYTHONIOENCODING=utf-8"}
	for _, key := range []string{"SYSTEMROOT", "WINDIR", "TEMP", "TMP", "PATH"} {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		cancel()
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	ready := make(chan []byte, 1)
	go func() { line, _ := bufio.NewReader(io.LimitReader(stdout, 1024)).ReadBytes('\n'); ready <- line }()
	select {
	case line := <-ready:
		var state struct {
			Version  int  `json:"schema_version"`
			Port     int  `json:"loopback_port"`
			Enabled  bool `json:"enabled"`
			Recorded bool `json:"recorded_only"`
		}
		if json.Unmarshal(line, &state) != nil || state.Version != 1 || !state.Enabled || !state.Recorded || state.Port < 1024 {
			t.Fatal("source process did not return bounded readiness contract")
		}
		return fmt.Sprintf("http://127.0.0.1:%d", state.Port)
	case <-time.After(5 * time.Second):
		t.Fatal("source process startup timed out")
	}
	return ""
}

func cleanSourceFixture(t *testing.T, c *ent.Client, day time.Time) {
	t.Helper()
	cleanup := func() {
		ctx := context.Background()
		_, _ = c.MarketData.Delete().Where(marketdata.FreqEQ("1d"), marketdata.BarTimeEQ(day)).Exec(ctx)
		_, _ = c.DatasetRecord.Delete().Where(datasetrecord.EventTimeEQ(day.UTC())).Exec(ctx)
		_, _ = c.Outbox.Delete().Where(outbox.EventIDHasPrefix("sb:")).Exec(ctx)
	}
	cleanup()
	t.Cleanup(cleanup)
}

func TestRecordedPythonHTTPFull5370ToPostgresPreservesPrimaryAndDeduplicates(t *testing.T) {
	base := sourceHTTPProcess(t)
	c := sourceAdapterPG(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	q := archiveQuery()
	cleanSourceFixture(t, c, q.Start)
	source, e := NewTdxArchiveSource(base)
	if e != nil {
		t.Fatal(e)
	}
	batch, e := source.DailyBars(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if len(batch.Bars) != 5370 || !batch.CoverageUnknown {
		t.Fatal("30 samples mistaken for full archive", len(batch.Bars))
	}
	for _, bar := range batch.Bars {
		if bar.AvailableAt != nil || bar.Provenance.ProviderRawSHA256 != "dfca352934d3681cb9e8e5644f0492d02a562d0a13f751acbc1d85febdf970f5" || bar.Provenance.AcquisitionMode != "recorded_provider_bytes" || bar.ObservedAt == nil {
			t.Fatal("actual raw/unknown availability provenance lost")
		}
	}
	primary := batch.Bars[0]
	if _, e := c.MarketData.Create().SetSymbol(primary.Symbol).SetBarTime(primary.Time).SetFreq("1d").SetOpen(100).SetHigh(101).SetLow(99).SetClose(100).SetVolume(1000).SetAmount(100000).SetSource("tushare").Save(ctx); e != nil {
		t.Fatal(e)
	}
	repo := NewRepo(c)
	ready := biz.Ready{Domain: biz.DomainDailyBar, Source: source.Name(), TradeDate: "2025-01-06"}
	if n, e := repo.UpsertBars(ctx, batch.Bars, source.Name(), ready); e != nil || n != 5370 {
		t.Fatalf("full archive write n=%d err=%v", n, e)
	}
	repeated, e := source.DailyBars(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if n, e := repo.UpsertBars(ctx, repeated.Bars, source.Name(), ready); e != nil || n != 0 {
		t.Fatalf("repeat n=%d err=%v", n, e)
	}
	count, e := c.DatasetRecord.Query().Where(datasetrecord.SourceEQ(source.Name())).Count(ctx)
	if e != nil || count != 5370 {
		t.Fatal("revision count", count, e)
	}
	count, e = c.MarketData.Query().Where(marketdata.BarTimeEQ(q.Start), marketdata.FreqEQ("1d")).Count(ctx)
	if e != nil || count != 5370 {
		t.Fatal("current rows", count, e)
	}
	current, e := c.MarketData.Query().Where(marketdata.SymbolEQ(primary.Symbol), marketdata.BarTimeEQ(primary.Time), marketdata.FreqEQ("1d")).Only(ctx)
	if e != nil || current.Source != "tushare" || current.Close == nil || *current.Close != 100 {
		t.Fatal("optional source overwrote primary", e)
	}
	count, e = c.Outbox.Query().Where(outbox.EventIDHasPrefix("sb:")).Count(ctx)
	if e != nil || count != 1 {
		t.Fatal("outbox repeat was not deduplicated", count, e)
	}
	count, e = c.StockBasic.Query().Count(ctx)
	if e != nil || count != 0 {
		t.Fatal("history adapter fabricated security listing/ST", count, e)
	}
	row, e := c.DatasetRecord.Query().Where(datasetrecord.SourceEQ(source.Name())).First(ctx)
	if e != nil {
		t.Fatal(e)
	}
	record := dataset.Record{Domain: row.Domain, BusinessKey: row.BusinessKey, Source: row.Source, EventTime: row.EventTime, AvailableAt: row.AvailableAt, Availability: row.Availability, Evidence: row.AvailabilityEvidence, ObservedAt: row.ObservedAt, RawHash: row.RawHash, RevisionHash: row.RevisionHash, Payload: row.Payload}
	if e = record.Verify(); e != nil {
		t.Fatal(e)
	}
	if row.AvailableAt != nil || row.Availability != "unknown" || row.ObservedAt.Before(time.Now().Add(-time.Minute)) || row.RawHash == batch.Bars[0].Provenance.ProviderRawSHA256 {
		t.Fatal("provider raw hash confused with normalized payload or collection clock")
	}
	macro, _ := NewOpenBBFredSource(base)
	if _, e = macro.Macro(ctx); e == nil {
		t.Fatal("missing FRED key fabricated macro rows")
	}
	t.Logf("actual recorded archive HTTP -> Go -> PG: 5370 rows, primary preserved, 1 event, repeat no-op; macro unavailable")
}

func TestPostgresSupplementConcurrentReservationRevisionAndRollback(t *testing.T) {
	c := sourceAdapterPG(t)
	ctx := context.Background()
	q := archiveQuery()
	q.Start = q.Start.AddDate(0, 0, 1)
	q.End = q.Start
	q.Expect = q.Start
	cleanSourceFixture(t, c, q.Start)
	observed := time.Now()
	provenance := &dataset.Provenance{Provider: "tdx.com.cn", ProviderRawSHA256: sourceHash([]byte("explicit synthetic transaction fixture")), ProviderRawKind: "provider_http_zip_bytes", RecordsSHA256: sourceHash([]byte("synthetic rows")), AcquisitionMode: "recorded_provider_bytes"}
	b := biz.Bar{Availability: dataset.Availability{ObservedAt: &observed, Provenance: provenance, Evidence: "synthetic correction and failure injection fixture"}, Symbol: "600001.SH", Time: q.Start, Freq: "1d", Open: 10, High: 11, Low: 9, Close: 10, Volume: 1000, Amount: 10000}
	ready := biz.Ready{Domain: biz.DomainDailyBar, Source: biz.SourceTdxArchive, TradeDate: q.Start.Format("2006-01-02")}
	repo := NewRepo(c)
	var wg sync.WaitGroup
	var inserted, failures atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, e := repo.UpsertBars(ctx, []biz.Bar{b}, biz.SourceTdxArchive, ready)
			if e != nil {
				failures.Add(1)
				t.Log(e)
			}
			if n == 1 {
				inserted.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 || inserted.Load() != 1 {
		t.Fatalf("unique reservation failures=%d winners=%d", failures.Load(), inserted.Load())
	}
	b.Close = 10.5
	fresh := *provenance
	fresh.ProviderRawSHA256 = sourceHash([]byte("explicit synthetic corrected response"))
	b.Provenance = &fresh
	if n, e := repo.UpsertBars(ctx, []biz.Bar{b}, biz.SourceTdxArchive, ready); e != nil || n != 1 {
		t.Fatal("correction", n, e)
	}
	count, e := c.DatasetRecord.Query().Where(datasetrecord.SourceEQ(biz.SourceTdxArchive), datasetrecord.EventTimeEQ(q.Start.UTC())).Count(ctx)
	if e != nil || count != 2 {
		t.Fatal("correction did not append vintage", count, e)
	}
	current, e := c.MarketData.Query().Where(marketdata.SymbolEQ(b.Symbol), marketdata.BarTimeEQ(q.Start)).Only(ctx)
	if e != nil || current.Close == nil || *current.Close != 10.5 {
		t.Fatal("same supplement correction failed", e)
	}
	before, _ := c.Outbox.Query().Where(outbox.EventIDHasPrefix("sb:")).Count(ctx)
	c.MarketData.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
			return nil, errors.New("forced supplemental current write failure")
		})
	})
	b.Close = 10.6
	if _, e := repo.UpsertBars(ctx, []biz.Bar{b}, biz.SourceTdxArchive, ready); e == nil {
		t.Fatal("forced failure did not roll back")
	}
	after, _ := c.Outbox.Query().Where(outbox.EventIDHasPrefix("sb:")).Count(ctx)
	count, _ = c.DatasetRecord.Query().Where(datasetrecord.SourceEQ(biz.SourceTdxArchive), datasetrecord.EventTimeEQ(q.Start.UTC())).Count(ctx)
	if before != after || count != 2 {
		t.Fatal("revision/outbox escaped rollback")
	}
}
