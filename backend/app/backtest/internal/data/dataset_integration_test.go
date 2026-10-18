package data

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"server/app/backtest/internal/biz"
	"server/conf"
	"server/ent"
	"server/ent/backtestreport"
	"server/ent/backtesttask"
	"server/ent/backtesttrade"
	"server/ent/datasetmanifest"
	"server/ent/datasetrecord"
	"server/ent/marketdata"
	"server/pkg/backtest"
	"server/pkg/dbinit"
	"server/pkg/entclient"
)

func datasetClient(t *testing.T) *ent.Client {
	t.Helper()
	value := os.Getenv("NT_TEST_PG_PORT")
	if value == "" {
		t.Skip("set NT_TEST_PG_PORT for isolated PostgreSQL dataset integration")
	}
	port, e := strconv.Atoi(value)
	if e != nil || port < 1 || port > 65535 {
		t.Fatal("invalid NT_TEST_PG_PORT")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, e := entclient.Open(ctx, entclient.Config{DB: dbinit.Config{Host: "127.0.0.1", Port: int32(port), User: "postgres", Password: "postgres", Database: "novatrader_dataset_replay_test", SSLMode: "disable", Timeout: 3 * time.Second}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c.Client
}

func TestPostgresFreezeQueueReplayImmutable(t *testing.T) {
	client := datasetClient(t)
	ctx := context.Background()
	fixture, days := pitFixture(t)
	hashes := []string{}
	for _, r := range fixture.Records {
		e := client.DatasetRecord.Create().SetDomain(r.Domain).SetBusinessKey(r.BusinessKey).SetSource(r.Source).SetEventTime(r.EventTime).SetNillableAvailableAt(r.AvailableAt).SetAvailability(r.Availability).SetAvailabilityEvidence(r.Evidence).SetObservedAt(r.ObservedAt).SetRawHash(r.RawHash).SetRevisionHash(r.RevisionHash).SetPayload(r.Payload).OnConflictColumns(datasetrecord.FieldRevisionHash).DoNothing().Exec(ctx)
		if e != nil {
			t.Fatal(e)
		}
		hashes = append(hashes, r.RevisionHash)
	}
	var ids []int
	var manifests []string
	t.Cleanup(func() {
		_, _ = client.BacktestTrade.Delete().Where(backtesttrade.TaskIDIn(ids...)).Exec(ctx)
		_, _ = client.BacktestReport.Delete().Where(backtestreport.TaskIDIn(ids...)).Exec(ctx)
		_, _ = client.BacktestTask.Delete().Where(backtesttask.IDIn(ids...)).Exec(ctx)
		_, _ = client.DatasetManifest.Delete().Where(datasetmanifest.ManifestIDIn(manifests...)).Exec(ctx)
		_, _ = client.DatasetRecord.Delete().Where(datasetrecord.RevisionHashIn(hashes...)).Exec(ctx)
	})
	src := NewSource(client)
	repo := NewRepo(client)
	settings := biz.NewSettings(&conf.Backtest{})
	settings.MonteCarloRuns = 10
	uc := biz.NewUsecase(repo, src, nil, settings, log.NewStdLogger(io.Discard))
	cfg := backtest.DefaultConfig()
	cfg.Strategy = "breakout_ref"
	cfg.Freq = "1d"
	cfg.Start = days[30].Format("2006-01-02")
	cfg.End = days[59].Format("2006-01-02")
	cfg.HistoryBars = 30
	cfg.LookaheadCheck = true
	raw, _ := json.Marshal(cfg)
	task, e := uc.CreateTask(ctx, biz.CreateRequest{Config: raw, CreatedBy: "PIT integration fixture"})
	if e != nil {
		t.Fatal(e)
	}
	ids = append(ids, task.ID)
	manifests = append(manifests, task.Spec.ManifestID)
	if task.Spec.ManifestID == "" || task.Spec.ManifestHash == "" {
		t.Fatal("queued task missing fixed manifest")
	}
	m, e := src.GetDataset(ctx, task.Spec.ManifestID)
	if e != nil {
		t.Fatal(e)
	}
	if m.Hash != task.Spec.ManifestHash {
		t.Fatal("JSONB/time round trip changed manifest identity")
	}
	if _, e = uc.RunNext(ctx); e != nil {
		t.Fatal(e)
	}
	done, e := uc.GetTask(ctx, task.ID)
	if e != nil || done.Status != biz.StatusSucceeded {
		t.Fatalf("first run: task=%+v err=%v", done, e)
	}
	// Simulate mutable source drift after the original task has been queued and executed.
	_, e = client.MarketData.Create().SetSymbol("600001.SH").SetBarTime(days[40]).SetFreq("1d").SetOpen(999).SetHigh(1000).SetLow(998).SetClose(999).SetVolume(100).SetAmount(99900).SetSource("pit-drift").Save(ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, _ = client.MarketData.Delete().Where(marketdata.SourceEQ("pit-drift")).Exec(ctx)
	})
	newTask, e := uc.RerunTask(ctx, task.ID, "PIT integration fixture")
	if e != nil {
		t.Fatal(e)
	}
	ids = append(ids, newTask.ID)
	if newTask.Spec.ManifestID != task.Spec.ManifestID || newTask.Spec.ManifestHash != task.Spec.ManifestHash {
		t.Fatal("rerun silently refroze mutable data")
	}
	if _, e = uc.RunNext(ctx); e != nil {
		t.Fatal(e)
	}
	done2, e := uc.GetTask(ctx, newTask.ID)
	if e != nil || done2.Status != biz.StatusSucceeded {
		t.Fatalf("rerun: task=%+v err=%v", done2, e)
	}
	if done.DataHash != done2.DataHash || done.Summary.FillsHash != done2.Summary.FillsHash {
		t.Fatal("mutable table affected frozen replay")
	}
	report, e := uc.Report(ctx, newTask.ID)
	if e != nil {
		t.Fatal(e)
	}
	var rep backtest.Report
	if e = json.Unmarshal(report, &rep); e != nil {
		t.Fatal(e)
	}
	if rep.DatasetManifestHash != m.Hash || rep.Reproduce == nil || rep.Reproduce.Verdict != "复现一致" {
		t.Fatalf("report lost reproduction provenance: %+v", rep.Reproduce)
	}
}
