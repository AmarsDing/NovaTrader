package biz

import (
	"context"
	"testing"
	"time"

	"server/pkg/ashare"

	"github.com/go-kratos/kratos/v2/log"
)

type allow struct{}

func (allow) Check(context.Context, CheckIn) (CheckOut, error) {
	return CheckOut{Approved: true}, nil
}

type deny struct{ reason string }

func (d deny) Check(context.Context, CheckIn) (CheckOut, error) {
	return CheckOut{Approved: false, Reason: d.reason}, nil
}

func newEngine(t *testing.T, risk Checker) (*Engine, *MemStore) {
	t.Helper()
	mem := NewMemStore()
	e := NewEngine(mem, risk, Settings{InitialCash: 1_000_000, PaperDaysRequired: 20, LiveEnabled: false}, log.DefaultLogger)
	return e, mem
}

func bar() Quote {
	return Quote{Open: 10, High: 10.2, Low: 9.9, Close: 10.05, Volume: 100000, LimitUp: 11, LimitDown: 9}
}

func TestLimitUpDoesNotFill(t *testing.T) {
	e, _ := newEngine(t, allow{})
	q := Quote{Open: 11, High: 11, Low: 11, Close: 11, Volume: 50000, LimitUp: 11, LimitDown: 9}
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "b1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 11, Volume: 100, Quote: &q,
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusSubmitted || o.Filled != 0 || o.Reason != "涨停封板" {
		t.Fatalf("got %+v", o)
	}
}

func TestPartialFill(t *testing.T) {
	e, _ := newEngine(t, allow{})
	q := bar()
	q.Volume = 1000
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "p1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10.05, Volume: 200, Quote: &q,
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusPartial || o.Filled != 100 {
		t.Fatalf("got status %s filled %d reason %s", o.Status, o.Filled, o.Reason)
	}
}

func TestT1ThenSellAfterRoll(t *testing.T) {
	e, _ := newEngine(t, allow{})
	q := bar()
	if _, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "t1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10.05, Volume: 100, Quote: &q,
	}); err != nil {
		t.Fatal(err)
	}
	sell, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "t2", Account: "SIM", Symbol: "600519.SH", Side: "sell",
		Price: 10, Volume: 100, Quote: &q,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sell.Status != StatusRejected {
		t.Fatalf("same-day sell status %s reason %s", sell.Status, sell.Reason)
	}
	if _, err := e.RollDay(context.Background(), "SIM"); err != nil {
		t.Fatal(err)
	}
	sold, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "t3", Account: "SIM", Symbol: "600519.SH", Side: "sell",
		Price: 9.9, Volume: 100, Quote: &q,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sold.Status != StatusFilled || sold.Filled != 100 {
		t.Fatalf("next-day sell %+v", sold)
	}
}

func TestFeeDeducted(t *testing.T) {
	e, mem := newEngine(t, allow{})
	q := bar()
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "f1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10.05, Volume: 100, Quote: &q,
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusFilled {
		t.Fatal(o.Status, o.Reason)
	}
	cash, _, _ := mem.LoadCash(context.Background(), BookPaper)
	price := 10.01
	amount := price * 100
	fee := ashare.DefaultFee().Cost(amount, false)
	want := 1_000_000 - amount - fee
	if cash.Cash < want-0.02 || cash.Cash > want+0.02 {
		t.Fatalf("cash %.4f want %.4f fee %.4f", cash.Cash, want, fee)
	}
}

func TestCancelLeavesNoPosition(t *testing.T) {
	e, _ := newEngine(t, allow{})
	if _, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "c1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10, Volume: 100,
	}); err != nil {
		t.Fatal(err)
	}
	o, err := e.Cancel(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusCancelled {
		t.Fatal(o.Status)
	}
	q := bar()
	if _, err := e.ApplyBar(context.Background(), "SIM", "600519.SH", q); err != nil {
		t.Fatal(err)
	}
	pos, _ := e.Positions(context.Background(), "SIM")
	for _, p := range pos {
		if p.Quantity != 0 {
			t.Fatalf("position changed: %+v", p)
		}
	}
}

func TestRiskRejectAndIdempotent(t *testing.T) {
	e, mem := newEngine(t, deny{reason: "R12"})
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "r1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10, Volume: 100, SignalID: "sig-1", StrategyVersion: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusRejected || o.Reason != "R12" || o.SignalID != "sig-1" {
		t.Fatalf("%+v", o)
	}
	again, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "r1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10, Volume: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != StatusRejected {
		t.Fatal(again.Status)
	}
	if _, ok, _ := mem.LoadCash(context.Background(), BookPaper); ok {
		t.Fatal("rejected order should not create cash movement")
	}
}

func TestLiveBlockedUntilPaperDays(t *testing.T) {
	e, _ := newEngine(t, allow{})
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "l1", Account: "LIVE", Symbol: "600519.SH", Side: "buy",
		Price: 10, Volume: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusRejected {
		t.Fatalf("%+v", o)
	}
}

func TestReconcileHaltsBuys(t *testing.T) {
	e, _ := newEngine(t, allow{})
	q := bar()
	if _, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "h1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10.05, Volume: 100, Quote: &q,
	}); err != nil {
		t.Fatal(err)
	}
	ok, diffs, err := e.Reconcile(context.Background(), "SIM", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok || len(diffs) == 0 {
		t.Fatalf("ok %v diffs %v", ok, diffs)
	}
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "h2", Account: "SIM", Symbol: "000001.SZ", Side: "buy",
		Price: 10, Volume: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusRejected {
		t.Fatalf("%+v", o)
	}
}

func TestListOrdersAndFills(t *testing.T) {
	e, _ := newEngine(t, allow{})
	q := bar()
	if _, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "q1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10.05, Volume: 100, Quote: &q,
	}); err != nil {
		t.Fatal(err)
	}
	orders, err := e.Orders(context.Background(), "SIM")
	if err != nil || len(orders) != 1 || orders[0].ClientOrderID != "q1" {
		t.Fatalf("orders %+v %v", orders, err)
	}
	fills, err := e.Fills(context.Background(), "SIM")
	if err != nil || len(fills) != 1 || fills[0].Qty <= 0 || fills[0].CreatedAt.IsZero() {
		t.Fatalf("fills %+v %v", fills, err)
	}
}

func TestLiveConfirmWindow(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.set.LiveEnabled = true
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	if err := mem.Commit(context.Background(), Batch{Cash: &Cash{Book: BookPaper, Cash: 1_000_000, PaperDays: 20}}); err != nil {
		t.Fatal(err)
	}
	q := bar()
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "live1", Account: "LIVE", Symbol: "600519.SH", Side: "buy",
		Price: 10.05, Volume: 100, Source: "manual", Operator: "ada", Quote: &q,
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusPending || o.Filled != 0 {
		t.Fatalf("pending %+v", o)
	}
	found := false
	for _, ev := range mem.Events() {
		if ev.Subject != "notify.desktop" {
			continue
		}
		body, _ := ev.Payload.(map[string]any)
		if body["kind"] == "confirm" && body["account"] == "LIVE" && body["client_order_id"] == "live1" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing confirm event")
	}
	got, err := e.Confirm(context.Background(), "live1")
	if err != nil || got.Status != StatusSubmitted {
		t.Fatalf("confirm %+v %v", got, err)
	}

	held, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "live2", Account: "LIVE", Symbol: "600519.SH", Side: "buy",
		Price: 10, Volume: 100, Source: "manual", Operator: "ada",
	})
	if err != nil || held.Status != StatusPending {
		t.Fatalf("hold %+v %v", held, err)
	}
	if _, err := e.Cancel(context.Background(), "live2"); err != nil {
		t.Fatal(err)
	}
	cancelled, err := e.Get(context.Background(), "live2")
	if err != nil || cancelled.Status != StatusCancelled || cancelled.Reason != "确认已放弃" {
		t.Fatalf("cancel %+v %v", cancelled, err)
	}

	if _, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "live3", Account: "LIVE", Symbol: "000001.SZ", Side: "buy",
		Price: 10, Volume: 100, Source: "manual", Operator: "ada",
	}); err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return now.Add(31 * time.Second) }
	if err := e.ExpireConfirms(context.Background()); err != nil {
		t.Fatal(err)
	}
	expired, err := e.Get(context.Background(), "live3")
	if err != nil || expired.Status != StatusCancelled || expired.Reason != "确认超时作废" {
		t.Fatalf("expired %+v %v", expired, err)
	}
}

func TestWatchTimeoutChase(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.Local)
	e := NewEngine(NewMemStore(), allow{}, Settings{WorkTimeout: time.Minute, ChaseMax: 2, ChaseCap: 0.005}, log.DefaultLogger)
	e.now = func() time.Time { return now }
	q := bar()
	if _, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "hold", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10.05, Volume: 100, Quote: &q,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RollDay(context.Background(), "SIM"); err != nil {
		t.Fatal(err)
	}
	sell, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "ex", Account: "SIM", Symbol: "600519.SH", Side: "sell",
		Price: 10, Volume: 100, Source: "watch", Operator: "risk",
	})
	if err != nil || sell.Status != StatusSubmitted {
		t.Fatalf("watch %+v %v", sell, err)
	}
	if err := e.SweepWorking(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Get(context.Background(), "ex"); got.Status != StatusSubmitted {
		t.Fatalf("early sweep %+v", got)
	}

	now = now.Add(61 * time.Second)
	if err := e.SweepWorking(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := e.Get(context.Background(), "ex")
	if err != nil || got.Status != StatusCancelled || got.Reason != "超时撤单" {
		t.Fatalf("parent %+v %v", got, err)
	}
	child, err := e.Get(context.Background(), "ex#1")
	if err != nil || child.Status != StatusSubmitted || child.Price != 9.99 || child.Volume != 100 || child.Source != "watch" {
		t.Fatalf("child %+v %v", child, err)
	}

	manual, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "man", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 11, Volume: 100, Source: "manual", Operator: "ada",
		Quote: &Quote{Open: 11, High: 11, Low: 11, Close: 11, Volume: 50000, LimitUp: 11, LimitDown: 9},
	})
	if err != nil || manual.Status != StatusSubmitted {
		t.Fatalf("manual %+v %v", manual, err)
	}
	now = now.Add(2 * time.Minute)
	if err := e.SweepWorking(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stayed, _ := e.Get(context.Background(), "man"); stayed.Status != StatusSubmitted {
		t.Fatalf("manual was chased %+v", stayed)
	}
	second, err := e.Get(context.Background(), "ex#2")
	if err != nil || second.Price != 9.98 {
		t.Fatalf("second %+v %v", second, err)
	}
	now = now.Add(2 * time.Minute)
	if err := e.SweepWorking(context.Background()); err != nil {
		t.Fatal(err)
	}
	last, err := e.Get(context.Background(), "ex#2")
	if err != nil || last.Status != StatusCancelled || last.Reason != "超时撤单，已到追价上限" {
		t.Fatalf("cap %+v %v", last, err)
	}
	if _, err := e.Get(context.Background(), "ex#3"); err == nil {
		t.Fatal("chased past the limit")
	}
}
