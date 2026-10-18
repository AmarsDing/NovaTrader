package data

import (
	"context"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"server/app/trade/internal/biz"
	"server/ent/executionintent"
	"server/ent/tradecash"
	"server/ent/tradeledgerlock"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPostgresHundredIntentClaimsAndLedgerSerialization(t *testing.T) {
	c := accountTestClient(t)
	ctx := context.Background()
	repo := NewRepo(c, log.DefaultLogger)
	id := "ct_" + uuid.NewString()
	book := "ct_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = c.ExecutionIntent.Delete().Where(executionintent.ClientOrderID(id)).Exec(ctx)
		_, _ = c.TradeCash.Delete().Where(tradecash.Book(book)).Exec(ctx)
		_, _ = c.TradeLedgerLock.Delete().Where(tradeledgerlock.Book(book)).Exec(ctx)
	})
	var wg sync.WaitGroup
	var created, bad atomic.Int32
	for n := 0; n < 100; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, fresh, err := repo.ClaimIntent(ctx, biz.Intent{BusinessKey: id, ClientOrderID: id, RequestHash: "same", Book: book})
			if err != nil {
				t.Logf("ledger: %v",err)
				bad.Add(1)
			}
			if fresh {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 || created.Load() != 1 {
		t.Fatalf("errors=%d claims=%d", bad.Load(), created.Load())
	}
	_, fresh, err := repo.ClaimIntent(ctx, biz.Intent{BusinessKey: id, ClientOrderID: id, RequestHash: "changed", Book: book})
	if err != nil || fresh {
		t.Fatal("conflict lookup failed", err)
	}
	if err := repo.Commit(ctx, biz.Batch{Cash: &biz.Cash{Book: book, Cash: 1000}}); err != nil {
		t.Fatal(err)
	}
	bad.Store(0)
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := repo.WithLedger(ctx, book, func(txctx context.Context) error {
				cash, _, err := repo.LoadCash(txctx, book)
				if err != nil {
					return err
				}
				cash.Cash++
				return repo.Commit(txctx, biz.Batch{Cash: &cash})
			})
			if err != nil {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	cash, _, err := repo.LoadCash(ctx, book)
	if err != nil || bad.Load() != 0 || cash.Cash != 1020 {
		t.Fatalf("cash=%f errors=%d err=%v", cash.Cash, bad.Load(), err)
	}
}
