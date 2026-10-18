package data

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"server/app/trade/internal/biz"
	"server/ent/tradesignal"
)

func TestPostgresBoundSignalValidationNeedsMatchingLockedFacts(t *testing.T) {
	c := accountTestClient(t)
	ctx := context.Background()
	now := time.Now()
	s, err := c.TradeSignal.Create().SetBook("paper").SetSignalTime(now).SetStockCode("600519.SH").SetStockName("隔离身份测试").SetSignalType(tradesignal.SignalTypeBuy).SetEntryPrice(10).SetStopLoss(9).SetTakeProfit(11).SetSignalReasoning("synthetic only").SetConfidenceScore(80).SetStrategyVersionID(42).SetValidUntil(now.Add(time.Minute)).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.TradeSignal.DeleteOneID(s.ID).Exec(ctx) })
	r := NewRepo(c, log.DefaultLogger)
	req := biz.PlaceRequest{ClientOrderID: "synthetic-new-order", SignalID: strconv.Itoa(s.ID), StrategyVersion: "42", Symbol: "600519.SH", Side: "buy", Source: "manual"}
	if err = r.ValidateSignalOrder(ctx, req, "paper", now); err == nil {
		t.Fatal("signal validation without ledger transaction")
	}
	check := func(req biz.PlaceRequest, book string, at time.Time) error {
		return r.WithLedger(ctx, "global", func(c context.Context) error { return r.ValidateSignalOrder(c, req, book, at) })
	}
	if err = check(req, "paper", now); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*biz.PlaceRequest)
		book   string
		at     time.Time
	}{
		{"wrong_book", func(*biz.PlaceRequest) {}, "live", now},
		{"wrong_symbol", func(r *biz.PlaceRequest) { r.Symbol = "000001.SZ" }, "paper", now},
		{"wrong_side", func(r *biz.PlaceRequest) { r.Side = "sell" }, "paper", now},
		{"wrong_version", func(r *biz.PlaceRequest) { r.StrategyVersion = "41" }, "paper", now},
		{"unknown_version", func(r *biz.PlaceRequest) { r.StrategyVersion = "" }, "paper", now},
		{"trigger_is_not_signal", func(r *biz.PlaceRequest) { r.SignalID = "stop_loss" }, "paper", now},
		{"expired", func(*biz.PlaceRequest) {}, "paper", now.Add(2 * time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := req
			tc.change(&copy)
			if err := check(copy, tc.book, tc.at); err == nil {
				t.Fatal("mismatched signal accepted")
			}
		})
	}
	if _, err = c.TradeSignal.UpdateOneID(s.ID).SetStatus("done").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err = check(req, "paper", now); err == nil {
		t.Fatal("new order revived a done signal")
	}
	req.Source = "watch"
	req.BusinessKey = "chase:synthetic-new-order"
	if err = check(req, "paper", now.Add(2*time.Minute)); err != nil {
		t.Fatal("internal bounded chase could not follow an existing filled parent", err)
	}
	if _, err = c.TradeSignal.UpdateOneID(s.ID).SetStatus("expired").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err = check(req, "paper", now); err == nil {
		t.Fatal("chase revived an expired signal")
	}
}
