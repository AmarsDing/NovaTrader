package outbox

import (
	"context"
	"errors"
	box "server/ent/outbox"
	"server/pkg/events"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostgresLeasesExcludeConcurrentDispatchAndRecoverExpiry(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	env, _ := events.New("trade", events.SubjectTradeOrder, "lease-test", map[string]string{"id": "fixture"})
	tx, err := c.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = Insert(ctx, tx, env); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	defer c.Outbox.Delete().Where(box.EventID(env.EventID)).Exec(ctx)
	var calls atomic.Int32
	pub := publishFunc(func(context.Context, events.Envelope) error {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	var wg sync.WaitGroup
	var bad atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Dispatch(ctx, c, pub, 10); err != nil {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || bad.Load() != 0 {
		t.Fatalf("calls=%d errors=%d", calls.Load(), bad.Load())
	}
	// A crashed dispatcher leaves a lease; only expiry makes it claimable.
	row, err := c.Outbox.Query().Where(box.EventID(env.EventID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Outbox.UpdateOne(row).ClearPublishedAt().SetLeaseOwner("crashed").SetLeaseUntil(time.Now().Add(time.Hour)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := Dispatch(ctx, c, pub, 1); err != nil || n != 0 {
		t.Fatal("unexpired lease stolen", n, err)
	}
	if err = c.Outbox.UpdateOne(row).SetLeaseUntil(time.Now().Add(-time.Second)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := Dispatch(ctx, c, pub, 1); err != nil || n != 1 {
		t.Fatal("expired lease not recovered", n, err)
	}
}
func TestPostgresPoisonRowDoesNotBlockHealthyRow(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	a, _ := events.New("trade", events.SubjectTradeOrder, "poison-test", map[string]string{"id": "bad"})
	b, _ := events.New("trade", events.SubjectTradeOrder, "poison-test", map[string]string{"id": "good"})
	tx, err := c.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = Insert(ctx, tx, a); err != nil {
		t.Fatal(err)
	}
	if err = Insert(ctx, tx, b); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	defer c.Outbox.Delete().Where(box.EventIDIn(a.EventID, b.EventID)).Exec(ctx)
	if err = c.Outbox.Update().Where(box.EventID(a.EventID)).SetMetadata(map[string]any{"schema_version": 999}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := Dispatch(ctx, c, publishFunc(func(_ context.Context, e events.Envelope) error {
		if e.EventID != b.EventID {
			t.Fatal("poison published")
		}
		return nil
	}), 10)
	if err == nil || n != 1 {
		t.Fatal("healthy row blocked", n, err)
	}
	bad, err := c.Outbox.Query().Where(box.EventID(a.EventID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bad.DeadLetter || bad.PublishedAt != nil {
		t.Fatal("poison not quarantined")
	}
}

func TestDomainOf(t *testing.T) {
	cases := map[string]string{
		"trade.order":             "trade",
		"strategy.signal.updated": "strategy",
		"market.snapshot":         "market",
		"nodot":                   "nodot",
		"trade":                   "trade",
		".leading":                ".leading",
		"":                        "",
	}
	for subject, want := range cases {
		if got := DomainOf(subject); got != want {
			t.Fatalf("DomainOf(%q)=%q want %q", subject, got, want)
		}
	}
}

// 域隔离：老旧的失败域不堵也不拖慢新域；每批前置观测按域登记最老待发年龄。
func TestPostgresDomainIsolationAndOldestAge(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	old1, _ := events.New("trade", events.SubjectTradeOrder, "iso-test", map[string]string{"id": "old1"})
	old2, _ := events.New("trade", events.SubjectTradeFill, "iso-test", map[string]string{"id": "old2"})
	fresh, _ := events.New("notify", events.SubjectNotifyRequest, "iso-test", map[string]string{"id": "fresh"})
	staleAt := time.Now().UTC().Add(-10 * time.Minute)
	freshAt := time.Now().UTC().Add(-5 * time.Minute)
	tx, err := c.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []events.Envelope{old1, old2, fresh} {
		if err = Insert(ctx, tx, env); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	defer c.Outbox.Delete().Where(box.EventIDIn(old1.EventID, old2.EventID, fresh.EventID)).Exec(ctx)
	if err = c.Outbox.Update().Where(box.EventID(old1.EventID)).SetCreatedAt(staleAt).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err = c.Outbox.Update().Where(box.EventIDIn(old2.EventID, fresh.EventID)).SetCreatedAt(freshAt).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 域列由 Insert 派生落库。
	row, err := c.Outbox.Query().Where(box.EventID(fresh.EventID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if row.Domain != "notify" {
		t.Fatalf("domain=%q want notify", row.Domain)
	}
	var order []string
	n, err := Dispatch(ctx, c, publishFunc(func(_ context.Context, e events.Envelope) error {
		order = append(order, e.Subject)
		if e.Subject == events.SubjectTradeOrder || e.Subject == events.SubjectTradeFill {
			return errors.New("trade publisher down")
		}
		return nil
	}), 10)
	if err == nil || n != 1 {
		t.Fatalf("sent=%d err=%v", n, err)
	}
	// 轮询隔离：notify 必在前两个尝试内被送达，不被两个更老的 trade 行排队在哪。
	pos := -1
	for i, s := range order {
		if s == events.SubjectNotifyRequest {
			pos = i
		}
	}
	if pos < 0 || pos > 1 {
		t.Fatalf("notify delivered at position %d; fairness broken: %v", pos, order)
	}
	// 观测发生在批前：两域都在待发，年龄各自来自 created_at。
	ages := oldestPendingAge.Values()
	tradeAge, okT := ages["trade"]
	notifyAge, okN := ages["notify"]
	if !okT || !okN {
		t.Fatalf("missing per-domain ages: %v", ages)
	}
	if tradeAge < 590 || notifyAge < 290 || notifyAge >= tradeAge {
		t.Fatalf("ages trade=%v notify=%v want trade>=600>notify>=300", tradeAge, notifyAge)
	}
	// 清干净后下一批观测理应清零该域，不留陈旧分维。
	if err = c.Outbox.Update().Where(box.EventIDIn(old1.EventID, old2.EventID)).SetPublishedAt(time.Now().UTC()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = Dispatch(ctx, c, publishFunc(func(context.Context, events.Envelope) error { return nil }), 10); err != nil {
		t.Fatal(err)
	}
	if ages := oldestPendingAge.Values(); len(ages) != 0 {
		t.Fatalf("stale domains left behind: %v", ages)
	}
}
