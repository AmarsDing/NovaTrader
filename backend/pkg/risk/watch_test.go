package risk

import (
	"testing"
	"time"

	"server/pkg/tradecal"
)

func watchInput(last float64) WatchInput {
	in := base()
	in.Quote = quote("000001.SZ", 10.00)
	in.Quote.Last, in.Quote.Bid1, in.Quote.Ask1 = last, last-0.01, last
	return WatchInput{
		Input: in,
		Holding: Holding{
			Symbol: "000001.SZ", Quantity: 1000, Available: 1000, AvgCost: 10.00,
			StopPrice: 9.50, TakePrice: 11.00, HeldDays: 1,
		},
		High: 10.00,
	}
}

func wantExit(t *testing.T, w WatchInput, trigger string) Exit {
	t.Helper()
	e, ok := WatchPosition(w, Defaults())
	if trigger == "" {
		if ok {
			t.Fatalf("unexpected exit %+v", e)
		}
		return e
	}
	if !ok || e.Trigger != trigger {
		t.Fatalf("want %s, got ok=%v %+v", trigger, ok, e)
	}
	return e
}

func TestWatchTriggers(t *testing.T) {
	wantExit(t, watchInput(10.20), "")
	e := wantExit(t, watchInput(9.50), ExitStopLoss)
	if e.Volume != 1000 || e.Symbol != "000001.SZ" {
		t.Fatalf("%+v", e)
	}

	// 没有止损价时用成本 × 95%。
	w := watchInput(9.51)
	w.Holding.StopPrice = 0
	wantExit(t, w, "")
	w = watchInput(9.50)
	w.Holding.StopPrice = 0
	wantExit(t, w, ExitStopLoss)

	w = watchInput(10.20)
	w.Market.Index5mChange = -0.016
	wantExit(t, w, ExitIndexPlunge)

	w = watchInput(10.20)
	w.NegativeNews = true
	wantExit(t, w, ExitBadNews)

	// 最高 10.60（浮盈 6%），回撤到 10.25（3.3%）。
	w = watchInput(10.25)
	w.High = 10.60
	wantExit(t, w, ExitTrailing)
	w.High = 10.40 // 浮盈未到 5%，不启动移动止盈
	wantExit(t, w, "")

	wantExit(t, watchInput(11.00), ExitTakeProfit)

	// 时间止损只在尾盘。
	w = watchInput(10.05)
	w.Holding.HeldDays = 2
	wantExit(t, w, "")
	w.Session.Late = true
	wantExit(t, w, ExitTimeStop)

	// 止损优先于利空。
	w = watchInput(9.40)
	w.NegativeNews = true
	wantExit(t, w, ExitStopLoss)
}

func TestWatchGuards(t *testing.T) {
	w := watchInput(9.00)
	w.Holding.Available = 0 // 今天买的
	wantExit(t, w, "")

	w = watchInput(9.00)
	w.Holding.FrozenSell = 1000 // 已经在卖
	wantExit(t, w, "")

	w = watchInput(9.00)
	w.Session = tradecal.Session{Phase: tradecal.NoonBreak}
	wantExit(t, w, "")

	w = watchInput(9.00)
	w.Holding.FrozenSell = 400
	if e := wantExit(t, w, ExitStopLoss); e.Volume != 600 {
		t.Fatal(e.Volume)
	}
}

func TestExitPrice(t *testing.T) {
	// 买一 9.49 减 2 个价位。
	e := wantExit(t, watchInput(9.50), ExitStopLoss)
	if e.Price != 9.47 {
		t.Fatal(e.Price)
	}
	// 跌停 9.00，没有买一时挂跌停价。
	w := watchInput(9.00)
	w.Quote.Bid1 = 0
	if e := wantExit(t, w, ExitStopLoss); e.Price != 9.00 {
		t.Fatal(e.Price)
	}
	// 买一 9.10：笼子下限 8.92，跌停 9.00，取高者 9.00。
	w = watchInput(9.11)
	w.Quote.Bid1 = 9.10
	p := Defaults()
	p.ExitSlipTicks = 50
	if e, _ := WatchPosition(w, p); e.Price != 9.00 {
		t.Fatal(e.Price)
	}
	// 出价能直接通过 Check。
	w = watchInput(9.50)
	e = wantExit(t, w, ExitStopLoss)
	in := w.Input
	in.Order = Order{ClientID: "x", Account: SIM, Symbol: e.Symbol, Side: Sell, Price: e.Price, Volume: e.Volume, Source: Watch}
	in.Account.Holdings = map[string]Holding{e.Symbol: w.Holding}
	in.Now = in.Now.Add(time.Millisecond)
	if d := Check(in, Defaults()); !d.Approved {
		t.Fatal(d.Reason)
	}
}
