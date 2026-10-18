package data

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	"server/app/admin/internal/gateway"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/migrate"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/google/uuid"
)

// 隔离PG：与真实账本库分开的专用测试库。设了 NT_TEST_PG_PORT 却连不上直接 FAIL，
// 不用 SKIP 冒充 PG 验收（隔离纪律与 trade/data 合同测试同一口径）。
func adminContractClient(t *testing.T, database string) *ent.Client {
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
	cfg := dbinit.Config{Host: "127.0.0.1", Port: port, User: "postgres", Password: "postgres", Database: database, SSLMode: "disable", Timeout: 3 * time.Second}
	if err := dbinit.EnsureDatabase(ctx, cfg); err != nil {
		if os.Getenv("NT_TEST_PG_PORT") != "" {
			t.Fatal(err)
		}
		t.Skipf("isolated PostgreSQL unavailable: %v", err)
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
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// 回执持久往返：写入→按 event/trace 读取→新值回读有效→限长生效。
// 追加式留痕：同事件可多条，谁在前按 created_at 逆序；不改写、不回放别的表。
func TestDispositionRoundTripOnIsolatedPostgres(t *testing.T) {
	client := adminContractClient(t, "novatrader_admin_contract_test")
	s := &AuditStore{client: client}
	ctx := context.Background()
	tag := uuid.NewString()[:8]
	eventID := "evt-dispo-" + tag
	traceID := "tr-dispo-" + tag

	if got, err := s.ListDispositions(ctx, eventID, "", 10); err != nil || len(got) != 0 {
		t.Fatalf("empty read = %v %d", err, len(got))
	}
	first := gateway.Disposition{EventID: eventID, TraceID: traceID, Action: "acknowledged", Note: "第一条", Actor: "owner-a"}
	second := gateway.Disposition{EventID: eventID, TraceID: traceID, Action: "resolved", Note: "第二条", Actor: "owner-b"}
	if err := s.WriteDisposition(ctx, first); err != nil {
		t.Fatal(err)
	}
	// created_at 同刻也可复现：列表按 (created_at, id) 逆序，后写者（id 更大）恒在前。
	if err := s.WriteDisposition(ctx, second); err != nil {
		t.Fatal(err)
	}
	byEvent, err := s.ListDispositions(ctx, eventID, "", 10)
	if err != nil || len(byEvent) != 2 {
		t.Fatalf("by event = %v %d %+v", err, len(byEvent), byEvent)
	}
	if byEvent[0].Action != "resolved" || byEvent[1].Action != "acknowledged" {
		t.Fatalf("newest first expected: %+v", byEvent)
	}
	byTrace, err := s.ListDispositions(ctx, "", traceID, 10)
	if err != nil || len(byTrace) != 2 {
		t.Fatalf("by trace = %v %d", err, len(byTrace))
	}
	limited, err := s.ListDispositions(ctx, eventID, "", 1)
	if err != nil || len(limited) != 1 || limited[0].Action != "resolved" {
		t.Fatalf("limit = %v %+v", err, limited)
	}
	// 留痕不改 outbox 投递状态：本测试库 outbox 里不应存在任何由留痕写入的记录。
	n, err := client.Outbox.Query().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("disposition writes must not touch outbox, found %d rows", n)
	}
}
