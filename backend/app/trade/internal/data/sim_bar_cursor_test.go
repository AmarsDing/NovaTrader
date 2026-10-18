package data

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"server/app/trade/internal/biz"
	entoutbox "server/ent/outbox"
	"server/ent/simbarcursor"
	"server/ent/tradeorder"
	"server/pkg/events"
)

func TestPostgresSimBarCursorAtomicRollbackAndConcurrentRestart(t *testing.T) {
	c := accountTestClient(t)
	ctx := context.Background()
	repo := NewRepo(c, log.DefaultLogger)
	const sym = "699998.SH" // isolated test database only; no production provider/ledger.
	id := "bar_" + uuid.NewString()
	_, err := c.SimBarCursor.Delete().Where(simbarcursor.Book(biz.BookPaper), simbarcursor.Symbol(sym)).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = c.SimBarCursor.Delete().Where(simbarcursor.Book(biz.BookPaper), simbarcursor.Symbol(sym)).Exec(ctx)
		_, _ = c.TradeOrder.Delete().Where(tradeorder.ClientOrderID(id)).Exec(ctx)
		_, _ = c.Outbox.Delete().Where(entoutbox.TraceID(id)).Exec(ctx)
	})
	at := time.Now().UTC().Truncate(time.Minute)
	if _, err := repo.LastConsumedSimBar(ctx, biz.BookPaper, sym); !errors.Is(err, biz.ErrInvalid) {
		t.Fatalf("cursor read outside ledger allowed: %v", err)
	}
	injected := errors.New("injected after ledger and cursor writes")
	traceCtx := events.WithTraceID(ctx, id)
	err = repo.WithLedger(traceCtx, "global", func(txctx context.Context) error {
		if err := repo.Commit(txctx, biz.Batch{Orders: []biz.Order{{ClientOrderID: id, Book: biz.BookPaper, Symbol: sym, Side: "buy", Status: biz.StatusSubmitted, Price: 10, Volume: 100, CreatedAt: at}}, Events: []biz.Event{{Subject: events.SubjectTradeOrder, Payload: map[string]any{"client_order_id": id}}}}); err != nil {
			return err
		}
		if err := repo.AdvanceSimBar(txctx, biz.BookPaper, sym, at, 10); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("wrong rollback error: %v", err)
	}
	if _, found, err := repo.LoadOrder(ctx, id); err != nil || found {
		t.Fatalf("order survived rollback: %v %v", found, err)
	}
	if n, err := c.Outbox.Query().Where(entoutbox.TraceID(id)).Count(ctx); err != nil || n != 0 {
		t.Fatalf("outbox survived rollback: %d %v", n, err)
	}
	if n, err := c.SimBarCursor.Query().Where(simbarcursor.Book(biz.BookPaper), simbarcursor.Symbol(sym)).Count(ctx); err != nil || n != 0 {
		t.Fatalf("cursor survived rollback: %d %v", n, err)
	}
	var wg sync.WaitGroup
	var consumed, bad atomic.Int32
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			restarted := NewRepo(c, log.DefaultLogger)
			if err := restarted.WithLedger(ctx, "global", func(txctx context.Context) error {
				last, err := restarted.LastConsumedSimBar(txctx, biz.BookPaper, sym)
				if err != nil {
					return err
				}
				if !at.After(last) {
					return nil
				}
				if err := restarted.AdvanceSimBar(txctx, biz.BookPaper, sym, at, 10); err != nil {
					return err
				}
				consumed.Add(1)
				return nil
			}); err != nil {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if consumed.Load() != 1 || bad.Load() != 0 {
		t.Fatalf("consumed=%d errors=%d", consumed.Load(), bad.Load())
	}
	if err := repo.WithLedger(ctx, "global", func(txctx context.Context) error {
		last, err := repo.LastConsumedSimBar(txctx, biz.BookPaper, sym)
		if err != nil {
			return err
		}
		if !last.Equal(at) {
			t.Fatalf("restart lost cursor: %v", last)
		}
		if err := repo.AdvanceSimBar(txctx, biz.BookPaper, sym, at.Add(-time.Minute), 10); !errors.Is(err, biz.ErrConflict) {
			t.Fatalf("old minute allowed: %v", err)
		}
		return repo.AdvanceSimBar(txctx, biz.BookPaper, sym, at.Add(time.Minute), 10)
	}); err != nil {
		t.Fatal(err)
	}
}
