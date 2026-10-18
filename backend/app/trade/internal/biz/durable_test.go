package biz

import (
	"context"
	"errors"
	"github.com/go-kratos/kratos/v2/log"
	"sync"
	"sync/atomic"
	"testing"
)

type countRisk struct{ calls atomic.Int32 }

func (c *countRisk) Check(context.Context, CheckIn) (CheckOut, error) {
	c.calls.Add(1)
	return CheckOut{Approved: true}, nil
}
func TestHundredConcurrentSameIntentAcrossEngines(t *testing.T) {
	mem := NewMemStore()
	risk := &countRisk{}
	var wg sync.WaitGroup
	var bad atomic.Int32
	for n := 0; n < 100; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := NewEngine(mem, risk, Settings{}, log.DefaultLogger)
			_, err := e.Place(context.Background(), PlaceRequest{ClientOrderID: "one", Account: "SIM", Symbol: "600519.SH", Side: "buy", Price: 10, Volume: 100})
			if err != nil && !errors.Is(err, ErrUncertain) {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	rows, _ := mem.ListOrders(context.Background(), BookPaper, -1)
	if bad.Load() != 0 || len(rows) != 1 || risk.calls.Load() != 1 {
		t.Fatalf("bad=%d orders=%d risk=%d", bad.Load(), len(rows), risk.calls.Load())
	}
}
func TestChangedRequestAndInterruptedIntentNeverResubmit(t *testing.T) {
	e, m := newEngine(t, allow{})
	ctx := context.Background()
	req := PlaceRequest{ClientOrderID: "fixed", Account: "SIM", Symbol: "600519.SH", Side: "buy", Price: 10, Volume: 100}
	if _, err := e.Place(ctx, req); err != nil {
		t.Fatal(err)
	}
	req.Volume = 200
	if _, err := e.Place(ctx, req); !errors.Is(err, ErrConflict) {
		t.Fatal("different request accepted", err)
	}
	req.ClientOrderID = "crashed"
	hash := orderRequestHash(req, BookPaper)
	_, _, _ = m.ClaimIntent(ctx, Intent{BusinessKey: "order:crashed", ClientOrderID: "crashed", Book: BookPaper, RequestHash: hash, State: "unknown"})
	// Normalize the source in the same way as the production entry.
	req.Source = "auto"
	m.mu.Lock()
	v := m.intents["crashed"]
	v.RequestHash = orderRequestHash(req, BookPaper)
	m.intents["crashed"] = v
	m.mu.Unlock()
	if _, err := e.Place(ctx, req); !errors.Is(err, ErrUncertain) {
		t.Fatal("interrupted intent resubmitted", err)
	}
	if rows, _ := m.ListOrders(ctx, BookPaper, -1); len(rows) != 1 {
		t.Fatal("new order created")
	}
}

func TestOneSignalCannotSubmitUnderDifferentManualOrderIDs(t *testing.T) {
	e, mem := newEngine(t, allow{})
	req := PlaceRequest{ClientOrderID: "signal-first", Account: "SIM", Symbol: "600519.SH", Side: "buy", Price: 10, Volume: 100, Source: "manual", Operator: "tester", SignalID: "42"}
	if _, err := e.Place(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.ClientOrderID = "signal-second"
	if _, err := e.Place(context.Background(), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("same signal duplicated: %v", err)
	}
	orders, _ := mem.ListOrders(context.Background(), BookPaper, -1)
	if len(orders) != 1 {
		t.Fatalf("got %d orders", len(orders))
	}
}
