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
	"server/ent/kbdocument"
	box "server/ent/outbox"
	"server/ent/taskrun"
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

func TestInBackfillWindow(t *testing.T) {
	sh := tradecal.Shanghai()
	cases := map[time.Time]bool{
		time.Date(2026, 11, 2, 16, 9, 0, 0, sh):  false,
		time.Date(2026, 11, 2, 16, 10, 0, 0, sh): true,
		time.Date(2026, 11, 2, 23, 59, 0, 0, sh): true,
		time.Date(2026, 11, 3, 0, 9, 0, 0, sh):   true,
		time.Date(2026, 11, 3, 0, 10, 0, 0, sh):  false,
		time.Date(2026, 11, 3, 9, 30, 0, 0, sh):  false,
	}
	for now, want := range cases {
		if got := InBackfillWindow(now); got != want {
			t.Errorf("InBackfillWindow(%s) = %v", now, got)
		}
	}
}

// TestCaseBackfillJob 是 16:10 回填 job 的端到端：调度时刻触发、按 outbox 载荷补 kb_case、
// 四字段缺席跳过、已建案例去重、重跑幂等。用隔离 PG，缺席时跳过。
func TestCaseBackfillJob(t *testing.T) {
	r, client := testRunner(t)
	ctx := context.Background()
	ids := []int64{980011, 980012, 980013}
	cleanup := func() {
		for _, q := range []func() error{
			func() error { _, err := client.KbCase.Delete().Where(kbcase.SignalIDIn(ids...)).Exec(ctx); return err },
			func() error {
				_, err := client.KbDocument.Delete().Where(kbdocument.SourceIn(
					"signal:paper:980011", "signal:paper:980012", "signal:paper:980013")).Exec(ctx)
				return err
			},
			func() error {
				_, err := client.Outbox.Delete().Where(box.EventIDIn("bf-e1", "bf-e2", "bf-e3", "bf-e4")).Exec(ctx)
				return err
			},
			func() error { _, err := client.TaskRun.Delete().Where(taskrun.TaskNameEQ(backfillJob)).Exec(ctx); return err },
		} {
			if err := q(); err != nil {
				t.Fatal(err)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Now()
	sigT := time.Date(2026, 10, 20, 9, 41, 0, 0, tradecal.Shanghai())
	full := `{"signal_id":980011,"book":"paper","status":"closed","stock_code":"000011.SZ","stock_name":"测试票",` +
		`"strategy":"weak2strong","signal_type":"buy","signal_time":` + jsonQuote(sigT) + `,` +
		`"exit_price":11.2,"pnl_pct":3.2,"holding_days":1,"pattern":"强势回踩","emotion_phase":"发酵","attribution":"missed","missed":true}`
	missing := `{"signal_id":980012,"book":"paper","status":"closed","stock_code":"000012.SZ","signal_time":` + jsonQuote(sigT) + `,` +
		`"pattern":"首板","emotion_phase":"启动","attribution":"missed"}`
	fullDup := `{"signal_id":980013,"book":"paper","status":"closed","stock_code":"000013.SZ","signal_time":` + jsonQuote(sigT) + `,` +
		`"pattern":"首板","emotion_phase":"启动","attribution":"missed","missed":false}`
	pend := `{"signal_id":980012,"book":"paper","status":"pend","stock_code":"000012.SZ","signal_time":` + jsonQuote(sigT) + `}`
	mk := func(id, payload string, published bool) {
		c := client.Outbox.Create().SetEventID(id).SetSubject(events.SubjectSignal).SetSource("strategy").
			SetPayload([]byte(payload)).SetCreatedAt(now)
		if published {
			c.SetPublishedAt(now)
		}
		if err := c.Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mk("bf-e1", full, true)
	mk("bf-e2", missing, true)
	mk("bf-e3", fullDup, true)
	mk("bf-e4", pend, false) // 未投递，不在扫描范围

	// 980013 已被消费侧建过案例：回填必须去重而不是重建。
	if _, err := r.uc.RecordCase(ctx, kb.SignalCase{
		SignalID: 980013, Book: "paper", Status: "closed", StockCode: "000013.SZ",
		SignalTime: sigT, ClosedAt: sigT.Add(5 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	// 触发 16:10 时刻的 Tick（捏造调度时刻；任务窗口内）。
	slot := time.Date(now.Year(), now.Month(), now.Day(), 16, 12, 0, 0, tradecal.Shanghai())
	if err := r.backfillSched.Tick(ctx, slot); err != nil {
		t.Fatal(err)
	}

	row, err := client.KbCase.Query().Where(kbcase.BookEQ("paper"), kbcase.SignalIDEQ(980011)).Only(ctx)
	if err != nil {
		t.Fatalf("backfilled case missing: %v", err)
	}
	if row.Attribution != "missed" || row.Pattern != "强势回踩" || row.EmotionPhase != "发酵" || row.Outcome != kbcase.OutcomeWin {
		t.Fatalf("case = %+v", row)
	}
	if n := client.KbCase.Query().Where(kbcase.SignalIDEQ(980012)).CountX(ctx); n != 0 {
		t.Fatal("payload missing the four fields must be skipped")
	}
	if n := client.KbCase.Query().Where(kbcase.SignalIDEQ(980013)).CountX(ctx); n != 1 {
		t.Fatalf("existing case duplicated: %d rows", n)
	}
	if n := client.TaskRun.Query().Where(taskrun.TaskNameEQ(backfillJob), taskrun.StatusEQ("success")).CountX(ctx); n != 1 {
		t.Fatalf("backfill task_run success = %d, want 1", n)
	}
	// 同一调度时刻已过成功，重 Tick 不再执行。
	if err := r.backfillSched.Tick(ctx, slot); err != nil {
		t.Fatal(err)
	}
	if n := client.TaskRun.Query().Where(taskrun.TaskNameEQ(backfillJob)).CountX(ctx); n != 1 {
		t.Fatalf("backfill reran within same slot: %d runs", n)
	}

	// 直接重跑用例层：已建案例按 (book,signal_id) 去重，字段缺席信号继续只计数。
	res, err := r.uc.BackfillCases(ctx, events.SubjectSignal, now.Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 0 || res.Existing != 2 || res.Skipped != 1 {
		t.Fatalf("rerun = %+v, want created=0 existing=2 skipped=1", res)
	}
}

func jsonQuote(t time.Time) string {
	b, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}
	return string(b)
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
	c := &conf.Kb{ConsumeSignals: true}
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
	if _, err := client.KbCase.Delete().Where(kbcase.SignalIDIn(id, 990002)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 案例正文按分钟渲染时间，同分钟内重跑内容哈希会撞上历史残留文档（content_hash 唯一），一并清掉。
	if _, err := client.KbDocument.Delete().Where(kbdocument.SourceIn(
		"signal:paper:990001", "signal:paper:990002")).Exec(ctx); err != nil {
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
