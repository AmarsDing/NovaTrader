package appcfg

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/migrate"
	"server/pkg/params"
)

// pkg/appcfg 5 秒缓存与回执合同（N2-03 落地「配置页 app.* → strategy_config → 5s 缓存」，
// 实施记录只跑了 go test 空包）的包级钉死。PG 门禁沿用 pkg/params 的 configTestClient 模式；
// 用同包内 pokes（at/receipts/client 字段）代替真实睡眠，语义不依赖时钟巧合。

func appcfgTestClient(t *testing.T) *ent.Client {
	t.Helper()
	raw := os.Getenv("NT_TEST_PG_PORT")
	if raw == "" {
		t.Skip("requires isolated NT_TEST_PG_PORT")
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 || port == 5432 {
		t.Fatal("isolated non-default PostgreSQL port required")
	}
	cfg := dbinit.Config{Host: "127.0.0.1", Port: int32(port), User: "postgres", Password: "postgres", Database: "novatrader_appcfg_test", SSLMode: "disable", Timeout: 3 * time.Second}
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

func latestReceipt(t *testing.T, ctx context.Context, c *ent.Client, service string) params.Receipt {
	t.Helper()
	receipts, err := params.Receipts(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range receipts {
		if r.Service == service && r.Instance == params.InstanceID && r.Scope == "app_cache" {
			return r
		}
	}
	t.Fatalf("receipt for %s missing", service)
	return params.Receipt{}
}

// poke 在同包内直接改写缓存内部态（代替等待 5 秒 TTL 与 30 秒回执节流）。
func poke(t *testing.T, fn func(c *cache)) {
	t.Helper()
	cc := current.Load()
	if cc == nil {
		t.Fatal("cache not bound")
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	fn(cc)
}

func expireNow(t *testing.T) {
	t.Helper()
	poke(t, func(c *cache) { c.at = time.Now().Add(-2 * ttl) })
}

func TestAppcfgUnboundAndEmptyKey(t *testing.T) {
	current.Store(nil)
	ctx := context.Background()
	if got := Get(ctx, "app.brain.large.model"); got != "" {
		t.Fatalf("unbound Get = %q", got)
	}
	if got := Get(ctx, ""); got != "" {
		t.Fatalf("empty key = %q", got)
	}
	if snap := Snapshot(ctx); len(snap) != 0 {
		t.Fatalf("unbound snapshot = %v", snap)
	}
}

func TestAppcfgCacheTTLReceiptFailureAndClone(t *testing.T) {
	c := appcfgTestClient(t)
	Bind(c)
	t.Cleanup(func() { current.Store(nil) })
	ctx := context.Background()

	s, err := params.Read(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	rev1, err := params.SaveCAS(ctx, c, s.Revision, map[string]string{"app.brain.large.model": "m1"}, "seed")
	if err != nil {
		t.Fatal(err)
	}

	// 1. 首次读取落缓存并记 cache_loaded 回执（r1）。
	if got := Get(ctx, "app.brain.large.model"); got != "m1" {
		t.Fatalf("first read = %q", got)
	}
	r1 := latestReceipt(t, ctx, c, "brain")
	if r1.Revision != rev1.Revision || r1.Status != "cache_loaded" {
		t.Fatalf("first receipt = %+v", r1)
	}

	// 2. TTL 5 秒内不重查：值已改成 m2，读到的仍是缓存的 m1。
	// 「期内」用字段重写（at=now）代替真实流逝，与头注释的「不依赖时钟巧合」对齐。
	rev2, err := params.SaveCAS(ctx, c, rev1.Revision, map[string]string{"app.brain.large.model": "m2"}, "update")
	if err != nil {
		t.Fatal(err)
	}
	poke(t, func(cc *cache) { cc.at = time.Now() })
	if got := Get(ctx, "app.brain.large.model"); got != "m1" {
		t.Fatalf("within TTL must not re-query, got %q", got)
	}
	// ack 30s 节流的存在性钉（不钉数值）：若节流被删，期内 Get 会立刻重记回执、
	// observed_at 前移——revision/status 都不变，必须盯时间戳（receipt.go :33-38 的 upsert 口径）。
	if r := latestReceipt(t, ctx, c, "brain"); r.Revision != rev1.Revision || !r.ObservedAt.Equal(r1.ObservedAt) {
		t.Fatalf("throttled receipt must stay at r1 untouched: %+v", r)
	}

	// 3. TTL 过期重查读到 m2；版本号变化把回执节流清零（appcfg.go :77-79），
	// 即刻记 r2 的 cache_loaded——不必再等 30 秒。
	expireNow(t)
	if got := Get(ctx, "app.brain.large.model"); got != "m2" {
		t.Fatalf("after expiry = %q", got)
	}
	if r := latestReceipt(t, ctx, c, "brain"); r.Revision != rev2.Revision || r.Status != "cache_loaded" {
		t.Fatalf("revision change must reset the receipt throttle: %+v", r)
	}
	// 记下 ack 节流的内存时间戳：步 5 要用它证明「死库读时 Record 根本没被尝试」。
	var t3stamp time.Time
	poke(t, func(cc *cache) { t3stamp = cc.receipts["brain"] })
	if t3stamp.IsZero() {
		t.Fatal("in-memory ack stamp must exist after a successful reload")
	}

	// 4. Snapshot 返回的是克隆：改返回的 map 不能污染缓存（appcfg.go :113-119）。
	snap := Snapshot(ctx)
	snap["app.brain.large.model"] = "EVIL"
	if got := Get(ctx, "app.brain.large.model"); got != "m2" {
		t.Fatalf("snapshot tamper leaked into cache: %q", got)
	}
	if again := Snapshot(ctx); again["app.brain.large.model"] != "m2" {
		t.Fatalf("snapshot not re-cloned: %v", again)
	}

	// 5. 库读不到的失败读：返回上次快照 m2 并记 failed（appcfg.go :68-74），失败状态先留在 c.failed。
	// 此刻记不了回执的第一闸门是 30s 节流（距步 3 毫秒级，Record 根本没被尝试）——
	// 死库只是即便放行也会失败的二道保险；步 6 poke receipts=nil 正是绕开节流才落库。
	deadDB, err := sql.Open("pgx", "host=127.0.0.1 port=1 user=postgres password=postgres dbname=postgres sslmode=disable connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer deadDB.Close()
	dead := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, deadDB)))
	defer dead.Close()
	poke(t, func(cc *cache) { cc.client = dead })
	expireNow(t)
	if got := Get(ctx, "app.brain.large.model"); got != "m2" {
		t.Fatalf("failed read must serve the old snapshot, got %q", got)
	}
	poke(t, func(cc *cache) {
		if !cc.failed {
			t.Fatal("failed flag not set after a dead read")
		}
		if !cc.receipts["brain"].Equal(t3stamp) {
			t.Fatal("throttle must short-circuit Record on the failed read: stamp moved")
		}
	})

	// 6. 换活 client、TTL 不再过期、回执节流清零：读到缓存 m2，同时把 failed 回执落库。
	poke(t, func(cc *cache) {
		cc.client = c
		cc.receipts = nil
		cc.at = time.Now()
	})
	if got := Get(ctx, "app.brain.large.model"); got != "m2" {
		t.Fatalf("cached read with restored client = %q", got)
	}
	if r := latestReceipt(t, ctx, c, "brain"); r.Revision != rev2.Revision || r.Status != "failed" {
		t.Fatalf("failed must be receipted against the last loaded revision: %+v", r)
	}

	// 7. 恢复：过期后重查成功，failed 标志清掉。
	expireNow(t)
	if got := Get(ctx, "app.brain.large.model"); got != "m2" {
		t.Fatalf("recovered read = %q", got)
	}
	poke(t, func(cc *cache) {
		if cc.failed {
			t.Fatal("failed flag must clear after a successful reload")
		}
	})

	// 8. TTL 数值双界钉：N2-03 与 validate.go :27-33 的用户可见文案都把「5 秒」写成
	// 硬口径，常量本身漂移（改成 30s/50ms）必须红灯——4 秒仍在期内（吃到旧缓存）、
	// 6 秒已过期（重查新值），把 ttl 夹在 (4s, 6s]。
	if _, err := params.SaveCAS(ctx, c, rev2.Revision, map[string]string{"app.brain.large.model": "m3"}, "ttl-boundary"); err != nil {
		t.Fatal(err)
	}
	poke(t, func(cc *cache) { cc.at = time.Now().Add(-4 * time.Second) })
	if got := Get(ctx, "app.brain.large.model"); got != "m2" {
		t.Fatalf("4s must still be within TTL, got %q", got)
	}
	poke(t, func(cc *cache) { cc.at = time.Now().Add(-6 * time.Second) })
	if got := Get(ctx, "app.brain.large.model"); got != "m3" {
		t.Fatalf("6s must have expired TTL, got %q", got)
	}
}
