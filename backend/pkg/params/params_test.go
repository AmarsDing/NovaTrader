package params

import (
	"context"
	"database/sql"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"os"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/migrate"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func configTestClient(t *testing.T) *ent.Client {
	t.Helper()
	raw := os.Getenv("NT_TEST_PG_PORT")
	if raw == "" {
		t.Skip("requires isolated NT_TEST_PG_PORT")
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 || port == 5432 {
		t.Fatal("isolated non-default PostgreSQL port required")
	}
	cfg := dbinit.Config{Host: "127.0.0.1", Port: int32(port), User: "postgres", Password: "postgres", Database: "novatrader_config_revision_test", SSLMode: "disable", Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = dbinit.EnsureDatabase(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = dbinit.EnsureExtension(ctx, cfg, "vector"); err != nil {
		t.Fatal(err)
	}
	if err = migrate.Schema(ctx, cfg); err != nil {
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
	db.SetMaxOpenConns(12)
	c := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { c.Close() })
	return c
}
func TestRevisionCASRollbackAndReceipts(t *testing.T) {
	c := configTestClient(t)
	ctx := context.Background()
	s, err := Read(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	base, err := SaveCAS(ctx, c, s.Revision, map[string]string{"m06.select_score": "75", "app.brain.large.key": "fixture-secret"}, "test baseline")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var won, conflicts atomic.Int32
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := SaveCAS(ctx, c, base.Revision, map[string]string{"m06.select_score": fmt.Sprint(50 + i%50)}, "concurrent test")
			if err == nil {
				won.Add(1)
			} else if errors.Is(err, ErrConflict) {
				conflicts.Add(1)
			} else {
				t.Errorf("save: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if won.Load() != 1 || conflicts.Load() != 99 {
		t.Fatalf("winners=%d conflicts=%d", won.Load(), conflicts.Load())
	}
	current, err := Read(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RollbackCAS(ctx, c, current.Revision, base.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision <= current.Revision || restored.Values["m06.select_score"] != "75" {
		t.Fatalf("rollback reused old version or wrong snapshot")
	}
	historical, err := Version(ctx, c, base.Revision)
	if err != nil || historical.Values["m06.select_score"] != "75" {
		t.Fatal("immutable baseline changed")
	}
	if _, _, err = PutMany(WithBase(ctx, base.Revision), c, map[string]string{"m06.select_score": "80"}, "string", "stale legacy"); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy bypass: %v", err)
	}
	if err = Record(ctx, c, "risk", "risk_engine", restored.Revision, "applied"); err != nil {
		t.Fatal(err)
	}
	if err = Record(ctx, c, "risk", "risk_engine", base.Revision, "failed"); err != nil {
		t.Fatal(err)
	}
	receipts, err := Receipts(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range receipts {
		if r.Instance == InstanceID && r.Service == "risk" {
			found = true
			if r.Revision != restored.Revision || r.Status != "applied" {
				t.Fatal("late receipt overwrote newer load")
			}
		}
	}
	if !found {
		t.Fatal("receipt missing")
	}
}
