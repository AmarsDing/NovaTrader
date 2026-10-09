package kbjobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"server/app/intel/internal/biz/kb"
	"server/app/intel/internal/data/kbstore"
	"server/conf"
	"server/ent"
	"server/ent/kbcase"
	"server/pkg/dbinit"
	"server/pkg/events"
	"server/pkg/migrate"
	"server/pkg/scheduler"
	"server/pkg/tradecal"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go"
)

func TestInPurgeWindow(t *testing.T) {
	sh := tradecal.Shanghai()
	cases := map[time.Time]bool{
		time.Date(2026, 11, 1, 3, 29, 0, 0, sh):  false,
		time.Date(2026, 11, 1, 3, 30, 0, 0, sh):  true,
		time.Date(2026, 11, 3, 2, 29, 0, 0, sh):  true,
		time.Date(2026, 11, 3, 2, 30, 0, 0, sh):  false,
		time.Date(2026, 11, 15, 3, 30, 0, 0, sh): false,
	}
	for now, want := range cases {
		if got := InPurgeWindow(now); got != want {
			t.Errorf("InPurgeWindow(%s) = %v", now, got)
		}
	}
}

func testRunner(t *testing.T) (*Runner, *ent.Client) {
	t.Helper()
	ctx := context.Background()
	cfg := dbinit.Config{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres",
		Database: "novatrader_kbtest", SSLMode: "disable", Timeout: 3 * time.Second,
	}
	if err := dbinit.EnsureDatabase(ctx, cfg); err != nil {
		t.Skip(err)
	}
	if err := dbinit.EnsureExtension(ctx, cfg, "vector"); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Schema(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	dsn, _ := dbinit.BusinessDSN(cfg)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { client.Close() })
	c := &conf.Kb{}
	uc := kb.NewKnowledgeUsecase(kbstore.NewKnowledgeRepo(client), nil, c, log.DefaultLogger)
	return New(uc, scheduler.NewEntStore(client), nil, c, log.DefaultLogger), client
}

func signalMsg(t *testing.T, payload string) *nats.Msg {
	t.Helper()
	env, err := events.New("signal", events.SubjectSignal, "trace-1", json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	b, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return &nats.Msg{Subject: events.SubjectSignal, Data: b}
}

func TestHandleSignal(t *testing.T) {
	r, client := testRunner(t)
	ctx := context.Background()
	const id = 990001
	if _, err := client.KbCase.Delete().Where(kbcase.SignalIDEQ(id)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	count := func() int { return client.KbCase.Query().Where(kbcase.SignalIDEQ(id)).CountX(ctx) }

	r.handleSignal(ctx, signalMsg(t, `{"signal_id":990001,"book":"paper","status":"filled","stock_code":"000001.SZ","signal_time":"2026-10-08T09:41:03+08:00"}`))
	if count() != 0 {
		t.Fatal("non-terminal status must not create a case")
	}
	closed := `{"signal_id":990001,"book":"paper","status":"closed","stock_code":"000001.SZ","stock_name":"平安银行",
		"signal_time":"2026-10-08T09:41:03+08:00","pnl_pct":-1.2,"holding_days":1,"pattern":"强势回踩","extra":"ignored"}`
	r.handleSignal(ctx, signalMsg(t, closed))
	r.handleSignal(ctx, signalMsg(t, closed))
	if count() != 1 {
		t.Fatalf("kb_case rows = %d, want 1", count())
	}
	row := client.KbCase.Query().Where(kbcase.SignalIDEQ(id)).OnlyX(ctx)
	if row.Outcome != kbcase.OutcomeLoss || row.HoldingDays == nil || *row.HoldingDays != 1 || row.ClosedAt.IsZero() {
		t.Fatalf("case = %+v", row)
	}

	r.handleSignal(ctx, &nats.Msg{Data: []byte("not json")})
	r.handleSignal(ctx, signalMsg(t, `{"signal_id":"x"}`))
	r.handleSignal(ctx, signalMsg(t, `{"signal_id":990002,"book":"paper","status":"closed","stock_code":"000001.SZ"}`))
	if n := client.KbCase.Query().Where(kbcase.SignalIDEQ(990002)).CountX(ctx); n != 0 {
		t.Fatal("payload without signal_time must be rejected")
	}
}
