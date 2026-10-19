package sim

import (
	"context"
	"testing"

	"server/pkg/brokerif"
)

func TestBuyIsNotSellableUntilNextDay(t *testing.T) {
	book := New()
	ctx := context.Background()
	id, err := book.Submit(ctx, brokerif.Order{ClientID: "1", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10, Volume: 200})
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Fill(id, 10); err != nil {
		t.Fatal(err)
	}
	pos, err := book.Positions(ctx)
	if err != nil || len(pos) != 1 || pos[0].Available != 0 || pos[0].Quantity != 200 {
		t.Fatalf("%+v %v", pos, err)
	}
	if _, err := book.Submit(ctx, brokerif.Order{ClientID: "2", Symbol: "600519.SH", Side: brokerif.Sell, Price: 11, Volume: 200}); err == nil {
		t.Fatal("same-day sell should fail")
	}
	book.RollDay()
	if _, err := book.Submit(ctx, brokerif.Order{ClientID: "3", Symbol: "600519.SH", Side: brokerif.Sell, Price: 11, Volume: 200}); err != nil {
		t.Fatal(err)
	}
}

func TestBookSubmitValidationAndIdentity(t *testing.T) {
	book := New()
	ctx := context.Background()
	bad := []brokerif.Order{
		{ClientID: "", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10, Volume: 100},
		{ClientID: "v0", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10, Volume: 0},
		{ClientID: "p0", Symbol: "600519.SH", Side: brokerif.Buy, Price: 0, Volume: 100},
		// 主板非整手买单在入口拒绝。
		{ClientID: "lot", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10, Volume: 150},
		// 科创板起买 200 以下在入口拒绝。
		{ClientID: "star", Symbol: "688001.SH", Side: brokerif.Buy, Price: 10, Volume: 150},
		// 无持仓裸卖。
		{ClientID: "naked", Symbol: "600519.SH", Side: brokerif.Sell, Price: 10, Volume: 100},
	}
	for _, o := range bad {
		if _, err := book.Submit(ctx, o); err == nil {
			t.Fatalf("order must be rejected: %+v", o)
		}
	}
	// 北交所起买线（100 股）以上逐股申报在入口放行——与上面科创板 150 被拒形成板块不对称。
	if _, err := book.Submit(ctx, brokerif.Order{ClientID: "bj", Symbol: "430047.BJ", Side: brokerif.Buy, Price: 10, Volume: 150}); err != nil {
		t.Fatalf("BJ per-share buy above minimum must be accepted: %v", err)
	}
	if _, err := book.Submit(ctx, brokerif.Order{ClientID: "1", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Submit(ctx, brokerif.Order{ClientID: "1", Symbol: "000001.SZ", Side: brokerif.Buy, Price: 9, Volume: 100}); err == nil {
		t.Fatal("duplicate client id must be rejected")
	}
}

func TestBookCancelRules(t *testing.T) {
	book := New()
	ctx := context.Background()
	if err := book.Cancel(ctx, "ghost"); err == nil {
		t.Fatal("unknown order must not cancel")
	}
	id, err := book.Submit(ctx, brokerif.Order{ClientID: "c1", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10, Volume: 200})
	if err != nil {
		t.Fatal(err)
	}
	// 部分成交后仍可撤剩余。
	if err := book.FillQty(id, 100, 10); err != nil {
		t.Fatal(err)
	}
	if err := book.Cancel(ctx, id); err != nil {
		t.Fatalf("partial order must be cancellable: %v", err)
	}
	if err := book.FillQty(id, 100, 10); err == nil {
		t.Fatal("cancelled order must not fill further")
	}
	// 再次撤销幂等（状态机不改写已撤终态，也不报错）——sim 自选语义：
	// cancelled 终态静默成功，与 FR-09-02 client_order_id 幂等同向；
	// filled 终态再撤仍报错（见下），双终态不对称。
	if err := book.Cancel(ctx, id); err != nil {
		t.Fatalf("cancel is idempotent: %v", err)
	}
	// 全部成交的委托不能再撤。
	id2, err := book.Submit(ctx, brokerif.Order{ClientID: "c2", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10, Volume: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Fill(id2, 10); err != nil {
		t.Fatal(err)
	}
	if err := book.Cancel(ctx, id2); err == nil {
		t.Fatal("filled order must not be cancellable")
	}
}

func TestBookPartialFillTracksAverageCost(t *testing.T) {
	book := New()
	ctx := context.Background()
	id, err := book.Submit(ctx, brokerif.Order{ClientID: "b1", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10.4, Volume: 300})
	if err != nil {
		t.Fatal(err)
	}
	if err := book.FillQty(id, 100, 10.0); err != nil {
		t.Fatal(err)
	}
	if err := book.FillQty(id, 100, 10.2); err != nil {
		t.Fatal(err)
	}
	if pos := book.Position("600519.SH"); pos.Quantity != 200 || !almost(pos.AvgCost, 10.1) {
		t.Fatalf("avg cost after two partials: %+v", pos)
	}
	if err := book.FillQty(id, 100, 10.4); err != nil {
		t.Fatal(err)
	}
	if pos := book.Position("600519.SH"); pos.Quantity != 300 || !almost(pos.AvgCost, 10.2) {
		t.Fatalf("avg cost at full fill: %+v", pos)
	}
	if err := book.FillQty(id, 1, 10.4); err == nil {
		t.Fatal("filled order must not accept more fills")
	}
}

func TestBookSellUpdatesAvailableWithinAndAcrossDays(t *testing.T) {
	book := New()
	ctx := context.Background()
	id, err := book.Submit(ctx, brokerif.Order{ClientID: "b1", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10, Volume: 500})
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Fill(id, 10); err != nil {
		t.Fatal(err)
	}
	book.RollDay()
	sid, err := book.Submit(ctx, brokerif.Order{ClientID: "s1", Symbol: "600519.SH", Side: brokerif.Sell, Price: 10, Volume: 200})
	if err != nil {
		t.Fatal(err)
	}
	if err := book.FillQty(sid, 200, 10); err != nil {
		t.Fatal(err)
	}
	if pos := book.Position("600519.SH"); pos.Quantity != 300 || pos.Available != 300 {
		t.Fatalf("post-sell availability: %+v", pos)
	}
	// 非全清、非整百的零股卖出在入口被拒：150%100 ≠ available%100（0）且不整百
	// （ashare.CanSell 主板支）——主板零股只能整百卖出或随全清离场。
	if _, err := book.Submit(ctx, brokerif.Order{ClientID: "odd", Symbol: "600519.SH", Side: brokerif.Sell, Price: 10, Volume: 150}); err == nil {
		t.Fatal("odd-lot non-full-exit sell must be rejected at submit")
	}
	// 同日再卖 300（余量全清）合法；持仓归零后再卖 100 因 want>available 在入口被拒
	// （ashare.CanSell 超持仓支，非整手规则）。
	sid2, err := book.Submit(ctx, brokerif.Order{ClientID: "s2", Symbol: "600519.SH", Side: brokerif.Sell, Price: 10, Volume: 300})
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Fill(sid2, 10); err != nil {
		t.Fatal(err)
	}
	if pos := book.Position("600519.SH"); pos.Quantity != 0 {
		t.Fatalf("flat expected: %+v", pos)
	}
	if _, err := book.Submit(ctx, brokerif.Order{ClientID: "s3", Symbol: "600519.SH", Side: brokerif.Sell, Price: 10, Volume: 100}); err == nil {
		t.Fatal("selling beyond holdings must be rejected at submit")
	}
}

func TestBookPositionsSortedAndSkipFlat(t *testing.T) {
	book := New()
	ctx := context.Background()
	b1, _ := book.Submit(ctx, brokerif.Order{ClientID: "a", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10, Volume: 100})
	b2, _ := book.Submit(ctx, brokerif.Order{ClientID: "b", Symbol: "000001.SZ", Side: brokerif.Buy, Price: 20, Volume: 200})
	if err := book.Fill(b1, 10); err != nil {
		t.Fatal(err)
	}
	if err := book.Fill(b2, 20); err != nil {
		t.Fatal(err)
	}
	pos, err := book.Positions(ctx)
	if err != nil || len(pos) != 2 || pos[0].Symbol != "000001.SZ" || pos[1].Symbol != "600519.SH" {
		t.Fatalf("positions sorted by symbol: %+v", pos)
	}
	book.RollDay()
	s, err := book.Submit(ctx, brokerif.Order{ClientID: "s", Symbol: "000001.SZ", Side: brokerif.Sell, Price: 21, Volume: 200})
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Fill(s, 21); err != nil {
		t.Fatal(err)
	}
	pos, err = book.Positions(ctx)
	if err != nil || len(pos) != 1 || pos[0].Symbol != "600519.SH" {
		t.Fatalf("flat rows are skipped: %+v", pos)
	}
	book.RollDay()
	// 次日可用全部解锁。
	if p := book.Position("600519.SH"); p.Available != 100 {
		t.Fatalf("roll day unlocks T+1 quantity: %+v", p)
	}
}
