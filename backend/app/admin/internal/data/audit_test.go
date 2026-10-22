package data

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"server/app/admin/internal/gateway"
	"server/conf"
)

// TestClipUTF8Boundary：长中文串必须按字节裁且不切碎字符。
// 30 个三字节汉字 = 90 字节，n=64 落刀在字中间，应退到 21 字（63 字节）。
func TestClipUTF8Boundary(t *testing.T) {
	got := clip(strings.Repeat("用", 30), 64)
	if len(got) > 64 || !utf8.ValidString(got) {
		t.Fatalf("clip = %d bytes valid=%v", len(got), utf8.ValidString(got))
	}
	if n := len([]rune(got)); n != 21 {
		t.Fatalf("runes = %d, want 21", n)
	}
	if s := clip("ada", 64); s != "ada" {
		t.Fatalf("short = %q", s)
	}
}

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
