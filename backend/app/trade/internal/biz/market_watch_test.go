package biz

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"server/pkg/tradecal"
)

func TestSimulationWatchSymbolsUsesWholeLedgerWithoutDesktop(t *testing.T) {
	ctx := context.Background()
	mem := NewMemStore()
	if err := mem.Commit(ctx, Batch{
		Positions: []Position{{Book: BookPaper, Symbol: "600519.SH", Quantity: 100}, {Book: BookPaper, Symbol: "000002.SZ", Quantity: 0}, {Book: BookLive, Symbol: "000003.SZ", Quantity: 100}},
		Orders:    []Order{{ClientOrderID: "open", Book: BookPaper, Symbol: "000001.SZ", Status: StatusSubmitted}, {ClientOrderID: "dupe", Book: BookPaper, Symbol: "600519.SH", Status: StatusPartial}, {ClientOrderID: "old", Book: BookPaper, Symbol: "000004.SZ", Status: StatusCancelled}},
	}); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(mem, allow{}, Settings{}, log.DefaultLogger)
	got, err := e.SimulationWatchSymbols(ctx)
	if err != nil || !reflect.DeepEqual(got, []string{"000001.SZ", "600519.SH"}) {
		t.Fatalf("symbols=%v err=%v", got, err)
	}
}

func TestWatchSIMWaitsForFirstCompleteMinuteBeforeTimeout(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 10, 0, 20, 0, tradecal.Shanghai())
	mem := NewMemStore()
	if err := mem.Commit(ctx, Batch{Cash: &Cash{Book: BookPaper, Cash: 1000000, DayStartDate: "2026-10-12", DayStartEquity: 1000000}, Positions: []Position{{Book: BookPaper, Symbol: "600519.SH", Quantity: 100, Available: 100, AvgCost: 9, LastPrice: 10}}}); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(mem, allow{}, Settings{}, log.DefaultLogger)
	e.now = func() time.Time { return now }
	o, err := e.Place(ctx, PlaceRequest{ClientOrderID: "exit", Account: "SIM", Symbol: "600519.SH", Side: "sell", Price: 9.9, Volume: 100, Source: "watch", Operator: "risk"})
	if err != nil || o.Status != StatusSubmitted {
		t.Fatalf("order=%+v err=%v", o, err)
	}
	now = time.Date(2026, 10, 12, 10, 1, 5, 0, tradecal.Shanghai())
	q := Quote{Time: now.Add(-time.Minute - 5*time.Second), Open: 10, High: 10, Low: 10, Close: 10, Volume: 100000}
	if _, err = e.ApplyBar(ctx, "SIM", o.Symbol, q); err != nil {
		t.Fatal(err)
	}
	if fills, _ := e.Fills(ctx, "SIM"); len(fills) != 0 {
		t.Fatal("order used a bar starting before submission")
	}
	now = now.Add(15 * time.Second)
	if err = e.SweepWorking(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Get(ctx, o.ClientOrderID); got.Status != StatusSubmitted {
		t.Fatalf("chased before an eligible bar: %+v", got)
	}
	now = time.Date(2026, 10, 12, 10, 2, 5, 0, tradecal.Shanghai())
	q.Time = now.Add(-time.Minute - 5*time.Second)
	if _, err = e.ApplyBar(ctx, "SIM", o.Symbol, q); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Get(ctx, o.ClientOrderID); got.Status != StatusFilled || got.Filled != 100 {
		t.Fatalf("first complete minute did not fill: %+v", got)
	}
}

func TestWorkingDeadlineLunchAndLiveSemantics(t *testing.T) {
	at := time.Date(2026, 10, 12, 11, 29, 20, 0, tradecal.Shanghai())
	o := Order{Book: BookPaper, CreatedAt: at}
	want := time.Date(2026, 10, 12, 13, 2, 5, 0, tradecal.Shanghai())
	if got := workingDeadline(o, time.Minute); !got.Equal(want) {
		t.Fatalf("lunch deadline=%v want=%v", got, want)
	}
	o.Book = BookLive
	if got := workingDeadline(o, time.Minute); !got.Equal(at.Add(time.Minute)) {
		t.Fatalf("LIVE semantics changed: %v", got)
	}
}
