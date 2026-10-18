package biz

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"server/pkg/tradecal"
)

func TestSourceMinuteConsumedOnceAcrossConcurrentEnginesAndRestart(t *testing.T) {
	ctx := context.Background()
	mem := NewMemStore()
	now := time.Date(2026, 10, 12, 10, 0, 20, 0, tradecal.Shanghai())
	newE := func() *Engine {
		e := NewEngine(mem, allow{}, Settings{}, log.DefaultLogger)
		e.now = func() time.Time { return now }
		return e
	}
	e := newE()
	if _, err := e.Place(ctx, PlaceRequest{ClientOrderID: "partial", Account: "SIM", Symbol: "600519.SH", Side: "buy", Price: 10.05, Volume: 1000}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	q := Quote{Time: time.Date(2026, 10, 12, 10, 1, 0, 0, tradecal.Shanghai()), Open: 10, High: 10, Low: 10, Close: 10, Volume: 1000, LimitUp: 11, LimitDown: 9}
	var wg sync.WaitGroup
	var bad atomic.Int32
	for n := 0; n < 100; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := newE().ApplyBar(ctx, "SIM", "600519.SH", q); err != nil {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("concurrent errors=%d", bad.Load())
	}
	order, _ := e.Get(ctx, "partial")
	if order.Filled != 100 || order.Status != StatusPartial {
		t.Fatalf("duplicate minute reused liquidity: %+v", order)
	}
	// A different event identity or corrected cumulative volume is still the
	// same source minute. Old bars and a fresh process cannot consume it again.
	q.Volume = 100000
	if _, err := newE().ApplyBar(ctx, "SIM", "600519.SH", q); err != nil {
		t.Fatal(err)
	}
	q.Time = q.Time.Add(-time.Minute)
	if _, err := newE().ApplyBar(ctx, "SIM", "600519.SH", q); err != nil {
		t.Fatal(err)
	}
	if order, _ := e.Get(ctx, "partial"); order.Filled != 100 {
		t.Fatalf("replayed/corrected/old minute filled: %+v", order)
	}
	q.Time = q.Time.Add(2 * time.Minute)
	q.Volume = 1000
	if _, err := newE().ApplyBar(ctx, "SIM", "600519.SH", q); err != nil {
		t.Fatal(err)
	}
	if order, _ := e.Get(ctx, "partial"); order.Filled != 200 {
		t.Fatalf("next minute not consumed: %+v", order)
	}
}

type failMatchCommit struct {
	*MemStore
	fail bool
}

func (s *failMatchCommit) Commit(ctx context.Context, b Batch) error {
	if s.fail {
		return errors.New("injected matching commit failure")
	}
	return s.MemStore.Commit(ctx, b)
}

func TestFailedMatchingDoesNotAdvanceSourceMinute(t *testing.T) {
	ctx := context.Background()
	mem := &failMatchCommit{MemStore: NewMemStore()}
	e := NewEngine(mem, allow{}, Settings{}, log.DefaultLogger)
	now := time.Date(2026, 10, 12, 10, 0, 20, 0, tradecal.Shanghai())
	e.now = func() time.Time { return now }
	if _, err := e.Place(ctx, PlaceRequest{ClientOrderID: "retry", Account: "SIM", Symbol: "600519.SH", Side: "buy", Price: 10.05, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	q := Quote{Time: now.Truncate(time.Minute).Add(-time.Minute), Open: 10, High: 10, Low: 10, Close: 10, Volume: 10000}
	mem.fail = true
	if _, err := e.ApplyBar(ctx, "SIM", "600519.SH", q); err == nil {
		t.Fatal("injected failure ignored")
	}
	mem.fail = false
	if _, err := e.ApplyBar(ctx, "SIM", "600519.SH", q); err != nil {
		t.Fatal(err)
	}
	if order, _ := e.Get(ctx, "retry"); order.Status != StatusFilled {
		t.Fatalf("failed minute incorrectly consumed: %+v", order)
	}
}

func TestSourceMinuteSharesOneParticipationPoolInStableOrder(t *testing.T) {
	for _, secondSide := range []string{"buy", "sell"} {
		t.Run(secondSide, func(t *testing.T) {
			ctx := context.Background()
			mem := NewMemStore()
			now := time.Date(2026, 10, 12, 10, 0, 20, 0, tradecal.Shanghai())
			if err := mem.Commit(ctx, Batch{Cash: &Cash{Book: BookPaper, Cash: 1000000, DayStartDate: "2026-10-12", DayStartEquity: 1005000}, Positions: []Position{{Book: BookPaper, Symbol: "600519.SH", Quantity: 500, Available: 500, AvgCost: 10, LastPrice: 10}}}); err != nil {
				t.Fatal(err)
			}
			e := NewEngine(mem, allow{}, Settings{}, log.DefaultLogger)
			e.now = func() time.Time { return now }
			// Intentionally insert b before a with identical timestamps. Source
			// matching must use the stable timestamp+order-ID priority.
			price := 10.05
			if secondSide == "sell" {
				price = 9.95
			}
			if _, err := e.Place(ctx, PlaceRequest{ClientOrderID: "b", Account: "SIM", Symbol: "600519.SH", Side: secondSide, Price: price, Volume: 200}); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Place(ctx, PlaceRequest{ClientOrderID: "a", Account: "SIM", Symbol: "600519.SH", Side: "buy", Price: 10.05, Volume: 200}); err != nil {
				t.Fatal(err)
			}
			now = now.Add(2 * time.Minute)
			q := Quote{Time: time.Date(2026, 10, 12, 10, 1, 0, 0, tradecal.Shanghai()), Open: 10, High: 10, Low: 10, Close: 10, Volume: 3000, LimitUp: 11, LimitDown: 9}
			if _, err := e.ApplyBar(ctx, "SIM", "600519.SH", q); err != nil {
				t.Fatal(err)
			}
			a, _ := e.Get(ctx, "a")
			b, _ := e.Get(ctx, "b")
			if a.Filled != 200 || b.Filled != 100 {
				t.Fatalf("shared pool/priority wrong: a=%+v b=%+v", a, b)
			}
			fills, _ := e.Fills(ctx, "SIM")
			total := 0
			for _, f := range fills {
				total += f.Qty
			}
			if total != 300 {
				t.Fatalf("minute participation duplicated or multiplied twice: %d", total)
			}
		})
	}
}
