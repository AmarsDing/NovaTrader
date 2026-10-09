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
