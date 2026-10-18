package outbox

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"server/ent"
	entoutbox "server/ent/outbox"
	"server/pkg/dbinit"
	"server/pkg/events"
	"server/pkg/migrate"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestDispatcherNilBus(t *testing.T) {
	n, err := (&Dispatcher{}).Dispatch(context.Background(), 10)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, err = (*Dispatcher)(nil).Dispatch(context.Background(), 10)
	if err != nil || n != 0 {
		t.Fatalf("nil n=%d err=%v", n, err)
	}
}

func testClient(t *testing.T) *ent.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	port := int32(5432)
	if value := os.Getenv("NT_TEST_PG_PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			t.Fatal("invalid NT_TEST_PG_PORT")
		}
		port = int32(parsed)
	}
	cfg := dbinit.Config{
		Host: "127.0.0.1", Port: port, User: "postgres", Password: "postgres",
		Database: "novatrader_outbox_test", SSLMode: "disable", Timeout: 3 * time.Second,
	}
	if err := dbinit.EnsureDatabase(ctx, cfg); err != nil {
		if os.Getenv("NT_TEST_PG_PORT") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	if err := dbinit.EnsureExtension(ctx, cfg, "vector"); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Schema(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	dsn, err := dbinit.BusinessDSN(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { client.Close() })
	return client
}

type publishFunc func(context.Context, events.Envelope) error

func (f publishFunc) Publish(ctx context.Context, env events.Envelope) error {
	return f(ctx, env)
}

func TestDispatchMarksPublishedAndRetriesFailure(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()
	okEnv, err := events.New("trade", events.SubjectTradeFill, "trace-ok", map[string]string{"id": "1"})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Add(-time.Minute)
	okEnv.SourceTime, okEnv.EntityID, okEnv.EntityVersion, okEnv.CausationID = &stamp, "snapshot:test", 9, "parent-test"
	badEnv, err := events.New("trade", events.SubjectTradeFill, "trace-bad", map[string]string{"id": "2"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, okEnv); err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, badEnv); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = client.Outbox.Delete().Where(entoutbox.EventIDIn(okEnv.EventID, badEnv.EventID)).Exec(context.Background())
	})

	pub := publishFunc(func(_ context.Context, env events.Envelope) error {
		if env.EventID == okEnv.EventID && (env.SourceTime == nil || !env.SourceTime.Equal(stamp) || env.EntityID != "snapshot:test" || env.EntityVersion != 9 || env.CausationID != "parent-test") {
			t.Fatalf("database dispatch lost envelope metadata: %+v", env)
		}
		if env.EventID == badEnv.EventID {
			return errors.New("broker down")
		}
		return nil
	})
	if _, err := Dispatch(ctx, client, pub, 100); err == nil {
		t.Fatal("expected publish error")
	}
	bad, err := client.Outbox.Query().Where(entoutbox.EventIDEQ(badEnv.EventID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bad.PublishedAt != nil || bad.Attempts < 1 {
		t.Fatalf("failed row published_at=%v attempts=%d", bad.PublishedAt, bad.Attempts)
	}
	if bad.NextAttemptAt == nil {
		t.Fatal("retry has no backoff")
	}
	if err := client.Outbox.UpdateOne(bad).SetNextAttemptAt(time.Now().Add(-time.Second)).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := Dispatch(ctx, client, publishFunc(func(context.Context, events.Envelope) error { return nil }), 100); err != nil {
		t.Fatal(err)
	}
	bad, err = client.Outbox.Query().Where(entoutbox.EventIDEQ(badEnv.EventID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bad.PublishedAt == nil {
		t.Fatal("row still unpublished")
	}
	okRow, err := client.Outbox.Query().Where(entoutbox.EventIDEQ(okEnv.EventID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if okRow.PublishedAt == nil {
		t.Fatal("successful row was not marked")
	}
}

func TestMetadataSurvivesPersistenceAndLegacyRows(t *testing.T) {
	stamp := time.Date(2026, 10, 15, 1, 30, 0, 0, time.UTC)
	parent, _ := events.New("datahub", events.SubjectMarketSnapshot, "trace", map[string]string{"batch": "b"})
	parent.SourceTime = &stamp
	env, _ := events.NewWithContext(events.ContextWithEnvelope(context.Background(), parent), "trade", events.SubjectTradeAccount, map[string]int{"volume": 100})
	env.EntityID, env.EntityVersion = "SIM:default", 12
	env.ReceivedAt = &stamp
	meta, err := envelopeMetadata(env)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := meta["payload"]; exists {
		t.Fatal("payload duplicated in metadata")
	}
	row := &ent.Outbox{EventID: env.EventID, TraceID: env.TraceID, Source: env.Source, Subject: env.Subject, CreatedAt: env.Time.Add(time.Second), Payload: env.Payload, Metadata: meta}
	got, err := envelopeFromRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if got.CausationID != parent.EventID || got.EntityID != env.EntityID || got.EntityVersion != 12 || got.SchemaVersion != 1 || got.SourceTime == nil || !got.SourceTime.Equal(stamp) || !got.Time.Equal(env.Time) {
		t.Fatalf("metadata lost: %+v", got)
	}
	row.Metadata = nil
	legacy, err := envelopeFromRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SchemaVersion != 0 || legacy.SourceTime != nil || !legacy.Time.Equal(row.CreatedAt) {
		t.Fatalf("legacy clocks invented: %+v", legacy)
	}
	row.Metadata = map[string]any{"schema_version": 99}
	if _, err := envelopeFromRow(row); err == nil {
		t.Fatal("unsupported persisted version accepted")
	}
}
