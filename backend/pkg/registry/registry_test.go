package registry

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/migrate"
	"server/pkg/params"
	"server/pkg/scheduler"
	"server/pkg/tradecal"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestHeartbeatParamsAndTasks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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

	day := time.Date(2026, 10, 8, 0, 0, 0, 0, tradecal.Shanghai())
	if err := tradecal.Default.Sync(ctx, client, day, day); err != nil {
		t.Fatal(err)
	}
	if err := Beat(ctx, client, "admin", "local", "127.0.0.1:2001"); err != nil {
		t.Fatal(err)
	}
	alive, err := Alive(ctx, client, time.Now().Add(-time.Minute))
	if err != nil || len(alive) == 0 {
		t.Fatalf("alive=%d err=%v", len(alive), err)
	}
	if err := params.Put(ctx, client, "position_per_stock", "0.05", "float", "单票上限"); err != nil {
		t.Fatal(err)
	}
	active, err := params.ActiveID(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := params.Put(ctx, client, "position_per_stock", "0.04", "float", "单票上限"); err != nil {
		t.Fatal(err)
	}
	if err := params.Rollback(ctx, client, active); err != nil {
		t.Fatal(err)
	}
	got, err := params.Get(ctx, client, "position_per_stock")
	if err != nil || got != "0.05" {
		t.Fatalf("value=%s err=%v", got, err)
	}

	store := scheduler.EntStore{Client: client}
	ran := 0
	s := scheduler.New(tradecal.Default, store, scheduler.Job{
		Name: "m00-probe", Spec: "0 10 * * *", TradingDayOnly: true,
		Run: func(context.Context) error {
			ran++
			return nil
		},
	})
	when := time.Date(2026, 10, 8, 10, 5, 0, 0, tradecal.Shanghai())
	if err := s.Tick(ctx, when); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx, when.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ran != 1 {
		t.Fatalf("ran=%d", ran)
	}
}
