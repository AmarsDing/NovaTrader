package accountstate

import (
	"encoding/json"
	"server/pkg/risk"
	"server/pkg/tradecal"
	"testing"
	"time"
)

func TestOptionalValuationProvenanceDoesNotInventLegacyMarks(t *testing.T) {
	at := time.Date(2026, 10, 16, 15, 9, 50, 0, tradecal.Shanghai())
	s := Snapshot{Version: 1, Kind: "full", AccountType: risk.SIM, AccountID: "default", SnapshotID: "synthetic-proof", Generation: 1, AsOf: at, TradingDay: "2026-10-16", Cash: 900, Available: 900, Equity: 1000, DayStartKnown: true, DayStartEquity: 1000, Holdings: []risk.Holding{{Symbol: "600519.SH", Quantity: 10, Available: 10, AvgCost: 10, MarketValue: 100}}, OpenOrders: []risk.OpenOrder{}, ObservedOrders: []string{}, Reconciliation: Reconciliation{Status: "matched", AsOf: at}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(s)
	decoded, err := Decode(p)
	if err != nil || decoded.Valuation != nil {
		t.Fatal("legacy evidence was invented", err)
	}
	start := time.Date(2026, 10, 16, 14, 59, 0, 0, tradecal.Shanghai())
	s.Valuation = &ValuationProof{Version: 1, Source: "market.validated_minute_bar", Complete: true, Marks: []ValuationMark{{Symbol: "600519.SH", Quantity: 10, Price: 10, BarStart: start, BarEnd: start.Add(time.Minute)}}, Reasons: []string{}}
	p, _ = json.Marshal(s)
	if _, err = Decode(p); err != nil {
		t.Fatal("complete optional proof rejected", err)
	}
	s.Valuation.Marks[0].Price = 11
	if err = s.Validate(); err == nil {
		t.Fatal("valuation price did not match recorded holdings")
	}
	s.Valuation.Marks[0].Price = 10
	s.Valuation.Marks[0].BarEnd = start.Add(2 * time.Minute)
	if err = s.Validate(); err == nil {
		t.Fatal("bar end misrepresented source interval")
	}
}
