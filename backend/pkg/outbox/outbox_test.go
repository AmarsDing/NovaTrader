package outbox

import (
	"context"
	"database/sql"
	"errors"
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

func testClient(t *testing.T) *ent.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cfg := dbinit.Config{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres",
		Database: "novatrader", SSLMode: "disable", Timeout: 3 * time.Second,
	}
	if err := dbinit.EnsureDatabase(ctx, cfg); err != nil {
		t.Skip(err)
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
