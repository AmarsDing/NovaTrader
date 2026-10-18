package outbox

import (
	"context"
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
