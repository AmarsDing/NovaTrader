package data

import (
	"context"
	"errors"
	"os"
	"strconv"
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

func captureClient(t *testing.T) *ent.Client {
	t.Helper()
	v := os.Getenv("NT_TEST_PG_PORT")
	if v == "" {
		t.Skip("set NT_TEST_PG_PORT for isolated datahub transaction integration")
	}
	port, e := strconv.Atoi(v)
	if e != nil || port < 1 || port > 65535 {
		t.Fatal("invalid NT_TEST_PG_PORT")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, e := entclient.Open(ctx, entclient.Config{DB: dbinit.Config{Host: "127.0.0.1", Port: int32(port), User: "postgres", Password: "postgres", Database: "novatrader_dataset_capture_test", SSLMode: "disable", Timeout: 3 * time.Second}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c.Client
}

func TestDatasetRevisionCurrentViewAndOutboxTransaction(t *testing.T) {
	client := captureClient(t)
	ctx := context.Background()
	repo := NewRepo(client)
	d := time.Date(2026, 1, 6, 0, 0, 0, 0, shanghaiLoc())
	a := d.Add(15 * time.Hour)
	const sym = "600001.SH"
	source := "pit-capture"
	key := sym + "/1d/" + d.UTC().Format(time.RFC3339Nano)
	t.Cleanup(func() {
		_, _ = client.MarketData.Delete().Where(marketdata.SourceEQ(source)).Exec(ctx)
		_, _ = client.DatasetRecord.Delete().Where(datasetrecord.SourceEQ(source)).Exec(ctx)
		_, _ = client.Outbox.Delete().Where(outbox.SourceEQ("datahub")).Exec(ctx)
	})
	b := biz.Bar{Availability: dataset.Availability{AvailableAt: &a, Evidence: "fixture exchange publication timestamp"}, Symbol: sym, Time: d, Freq: "1d", Open: 10, High: 11, Low: 9, Close: 10, Volume: 10000, Amount: 100000}
	ready := biz.Ready{Domain: "daily_bar", TradeDate: "2026-01-06", Source: source}
	for i := 0; i < 2; i++ {
		if _, e := repo.UpsertBars(ctx, []biz.Bar{b}, source, ready); e != nil {
			t.Fatal(e)
		}
	}
	n, e := client.DatasetRecord.Query().Where(datasetrecord.SourceEQ(source), datasetrecord.BusinessKeyEQ(key)).Count(ctx)
	if e != nil || n != 1 {
		t.Fatalf("repeat collection changed revision identity: %d %v", n, e)
	}
	rows, e := client.DatasetRecord.Query().Where(datasetrecord.SourceEQ(source)).All(ctx)
	if e != nil {
		t.Fatal(e)
	}
	r := rows[0]
	record := dataset.Record{Domain: r.Domain, BusinessKey: r.BusinessKey, Source: r.Source, EventTime: r.EventTime, AvailableAt: r.AvailableAt, Availability: r.Availability, Evidence: r.AvailabilityEvidence, ObservedAt: r.ObservedAt, RawHash: r.RawHash, RevisionHash: r.RevisionHash, Payload: r.Payload}
	if e = record.Verify(); e != nil {
		t.Fatalf("stored record failed integrity: %v", e)
	}
	// A later provider correction is an additional vintage, while the current view updates.
	late := a.AddDate(0, 0, 1)
	b.AvailableAt = &late
	b.Close = 10.5
	if _, e = repo.UpsertBars(ctx, []biz.Bar{b}, source, ready); e != nil {
		t.Fatal(e)
	}
	n, e = client.DatasetRecord.Query().Where(datasetrecord.SourceEQ(source)).Count(ctx)
	if e != nil || n != 2 {
		t.Fatalf("revision was overwritten: %d %v", n, e)
	}
	before, e := client.Outbox.Query().Where(outbox.SourceEQ("datahub")).Count(ctx)
	if e != nil {
		t.Fatal(e)
	}
	// A market write failure happens after revision insertion; the new revision and event must roll back.
	client.MarketData.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			return nil, errors.New("forced current-view write failure")
		})
	})
	b.Close = 11
	if _, e = repo.UpsertBars(ctx, []biz.Bar{b}, source, ready); e == nil {
		t.Fatal("expected write failure")
	}
	n, e = client.DatasetRecord.Query().Where(datasetrecord.SourceEQ(source)).Count(ctx)
	if e != nil || n != 2 {
		t.Fatalf("revision escaped rollback: %d %v", n, e)
	}
	after, e := client.Outbox.Query().Where(outbox.SourceEQ("datahub")).Count(ctx)
	if e != nil || after != before {
		t.Fatal("outbox escaped failed transaction")
	}
	current, e := client.MarketData.Query().Where(marketdata.SymbolEQ(sym), marketdata.FreqEQ("1d"), marketdata.BarTimeEQ(d)).Only(ctx)
	if e != nil || current.Close == nil || *current.Close != 10.5 {
		t.Fatal("current view changed after rollback")
	}
}
