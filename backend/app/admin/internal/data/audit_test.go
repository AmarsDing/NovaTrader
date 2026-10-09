package data

import (
	"context"
	"testing"
	"time"

	"server/app/admin/internal/gateway"
	"server/conf"
)

func TestAuditRoundTrip(t *testing.T) {
	store, cleanup, err := connectAudit(context.Background(), &conf.Postgres{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres",
		Database: "novatrader_admintest", SslMode: "disable",
	})
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()
	if _, err := store.client.AuditLog.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	err = store.Write(ctx, gateway.Entry{
		Actor: "ada", Role: "owner", Action: "trade.order", Target: "/api/trade/orders", Detail: "POST", OK: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.client.AuditLog.Query().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Actor != "ada" || !rows[0].Ok || rows[0].Action != "trade.order" {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].CreatedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("created_at %s", rows[0].CreatedAt)
	}
}
