// Package accountstate defines the versioned, full trade-to-risk ledger contract.
// A reconciliation notification is deliberately not an account snapshot.
package accountstate

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"server/pkg/risk"
	"server/pkg/symbol"
	"server/pkg/tradecal"
)

const Version = 1
const DefaultAccountID = "default"

// Snapshot is AccountSnapshotV1. Available excludes FrozenCash; Holding.Available
// excludes today's purchases but includes FrozenSell. OpenOrders.Volume is the
// unfilled remainder, not the original submitted quantity.
type Snapshot struct {
	Version        int              `json:"version"`
	Kind           string           `json:"kind"`
	AccountID      string           `json:"account_id"`
	AccountType    risk.AccountType `json:"account_type"`
	SnapshotID     string           `json:"snapshot_id"`
	Generation     int64            `json:"generation"`
	AsOf           time.Time        `json:"as_of"`
	TradingDay     string           `json:"trading_day"`
	Cash           float64          `json:"cash"`
	FrozenCash     float64          `json:"frozen_cash"`
	Equity         float64          `json:"equity"`
	Available      float64          `json:"available"`
	DayStartEquity float64          `json:"day_start_equity"`
	DayStartKnown  bool             `json:"day_start_known"`
	Holdings       []risk.Holding   `json:"holdings"`
	OpenOrders     []risk.OpenOrder `json:"open_orders"`
	ObservedOrders []string         `json:"observed_order_ids"`
	Reconciliation Reconciliation   `json:"reconciliation"`
	Valuation      *ValuationProof  `json:"valuation,omitempty"` // Optional provenance; legacy state remains unknown.
}

// ValuationProof binds each recorded holding mark to a server-validated closed
// minute bar. Complete means provenance coverage, never tick freshness or a
// closing valuation; consumers must check each bar date/end for their purpose.
type ValuationProof struct {
	Version  int             `json:"version"`
	Source   string          `json:"source"`
	Complete bool            `json:"complete"`
	Marks    []ValuationMark `json:"marks"`
	Reasons  []string        `json:"reasons"`
}
type ValuationMark struct {
	Symbol   string    `json:"symbol"`
	Quantity int       `json:"quantity"`
	Price    float64   `json:"price"`
	BarStart time.Time `json:"bar_start"`
	BarEnd   time.Time `json:"bar_end"`
}

type Reconciliation struct {
	Status string    `json:"status"` // matched, blocked, or unavailable
	AsOf   time.Time `json:"as_of"`
	Reason string    `json:"reason"`
}

// Decode rejects missing fields before decoding numbers (missing is not zero).
func Decode(raw []byte) (Snapshot, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Snapshot{}, err
	}
	if err := required(fields, "version", "kind", "account_id", "account_type", "snapshot_id", "generation", "as_of", "trading_day", "cash", "frozen_cash", "equity", "available", "day_start_equity", "day_start_known", "holdings", "open_orders", "observed_order_ids", "reconciliation"); err != nil {
		return Snapshot{}, err
	}
	for _, name := range []string{"holdings", "open_orders"} {
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(fields[name], &rows); err != nil {
			return Snapshot{}, err
		}
		for _, row := range rows {
			keys := []string{"symbol", "quantity", "available", "frozen_sell", "avg_cost", "market_value", "held_days"}
			if name == "open_orders" {
				keys = []string{"client_order_id", "symbol", "side", "price", "volume"}
			}
			if err := required(row, keys...); err != nil {
				return Snapshot{}, fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	var reconciliation map[string]json.RawMessage
	if err := json.Unmarshal(fields["reconciliation"], &reconciliation); err != nil {
		return Snapshot{}, err
	}
	if err := required(reconciliation, "status", "as_of", "reason"); err != nil {
		return Snapshot{}, err
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	return s, s.Validate()
}

func required(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		if b, ok := fields[name]; !ok || string(b) == "null" {
			return fmt.Errorf("account snapshot: missing %s", name)
		}
	}
	return nil
}

func finiteNonnegative(values ...float64) bool {
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return false
		}
	}
	return true
}

func (s Snapshot) Validate() error {
	if s.Version != Version || s.Kind != "full" {
		return fmt.Errorf("account snapshot: unsupported version or kind")
	}
	if s.AccountID != DefaultAccountID || (s.AccountType != risk.SIM && s.AccountType != risk.LIVE) {
		return fmt.Errorf("account snapshot: unsupported account identity")
	}
	if s.Generation <= 0 || s.SnapshotID == "" || s.AsOf.IsZero() || s.TradingDay != s.AsOf.In(tradecal.Shanghai()).Format("2006-01-02") {
		return fmt.Errorf("account snapshot: invalid identity/time")
	}
	if s.Holdings == nil || s.OpenOrders == nil || s.ObservedOrders == nil {
		return fmt.Errorf("account snapshot: collections must be explicit")
	}
	if !finiteNonnegative(s.Cash, s.FrozenCash, s.Equity, s.Available, s.DayStartEquity) || math.Abs(s.Cash-s.FrozenCash-s.Available) > 0.02 {
		return fmt.Errorf("account snapshot: invalid cash conservation")
	}
	if s.DayStartKnown && s.DayStartEquity <= 0 {
		return fmt.Errorf("account snapshot: invalid day-start equity")
	}
	if !s.DayStartKnown && s.DayStartEquity != 0 {
		return fmt.Errorf("account snapshot: unknown baseline must be zero")
	}
	if s.Reconciliation.AsOf.IsZero() || s.Reconciliation.AsOf.After(s.AsOf) {
		return fmt.Errorf("account snapshot: invalid reconciliation time")
	}
	switch s.Reconciliation.Status {
	case "matched", "blocked", "unavailable":
	default:
		return fmt.Errorf("account snapshot: invalid reconciliation state")
	}
	if s.Reconciliation.Status != "matched" && s.Reconciliation.Reason == "" {
		return fmt.Errorf("account snapshot: missing reconciliation reason")
	}
	seen := map[string]bool{}
	for _, id := range s.ObservedOrders {
		if id == "" || seen[id] {
			return fmt.Errorf("account snapshot: duplicate/empty observed order")
		}
		seen[id] = true
	}
	holdings := map[string]risk.Holding{}
	value := s.Cash
	for _, h := range s.Holdings {
		parsed, err := symbol.Parse(h.Symbol)
		if err != nil || parsed.Tongdaxin() != h.Symbol || h.Quantity <= 0 || h.Available < 0 || h.Available > h.Quantity || h.FrozenSell < 0 || h.FrozenSell > h.Available || h.HeldDays < 0 || h.AvgCost <= 0 || h.MarketValue <= 0 || !finiteNonnegative(h.AvgCost, h.MarketValue, h.StopPrice, h.TakePrice) {
			return fmt.Errorf("account snapshot: invalid holding %s", h.Symbol)
		}
		if _, ok := holdings[h.Symbol]; ok {
			return fmt.Errorf("account snapshot: duplicate holding")
		}
		holdings[h.Symbol] = h
		value += h.MarketValue
	}
	if math.Abs(value-s.Equity) > 0.02 {
		return fmt.Errorf("account snapshot: equity does not match cash and holdings")
	}
	openIDs := map[string]bool{}
	sells := map[string]int{}
	buyValue := 0.0
	for _, o := range s.OpenOrders {
		parsed, err := symbol.Parse(o.Symbol)
		if err != nil || parsed.Tongdaxin() != o.Symbol || o.ClientID == "" || !seen[o.ClientID] || openIDs[o.ClientID] || o.Volume <= 0 || !finiteNonnegative(o.Price) || o.Price == 0 || (o.Side != risk.Buy && o.Side != risk.Sell) {
			return fmt.Errorf("account snapshot: invalid open order")
		}
		openIDs[o.ClientID] = true
		if o.Side == risk.Sell {
			sells[o.Symbol] += o.Volume
		} else {
			buyValue += o.Price * float64(o.Volume)
		}
	}
	if s.FrozenCash+0.02 < buyValue {
		return fmt.Errorf("account snapshot: buy orders exceed frozen cash")
	}
	for sym, h := range holdings {
		if h.FrozenSell != sells[sym] {
			return fmt.Errorf("account snapshot: frozen shares mismatch %s", sym)
		}
		delete(sells, sym)
	}
	if len(sells) != 0 {
		return fmt.Errorf("account snapshot: sell order without holding")
	}
	return s.validateValuation()
}

func (s Snapshot) validateValuation() error {
	p := s.Valuation
	if p == nil {
		return nil
	}
	if p.Version != 1 || p.Marks == nil || p.Reasons == nil || !p.Complete && len(p.Reasons) == 0 || p.Complete && len(p.Reasons) != 0 {
		return fmt.Errorf("account snapshot: invalid valuation proof")
	}
	if p.Source == "ledger_cash_only" {
		if len(s.Holdings) != 0 || len(p.Marks) != 0 || !p.Complete {
			return fmt.Errorf("account snapshot: cash-only proof has holdings")
		}
		return nil
	}
	if p.Source != "market.validated_minute_bar" {
		return fmt.Errorf("account snapshot: unsupported valuation source")
	}
	holdings := map[string]risk.Holding{}
	for _, h := range s.Holdings {
		holdings[h.Symbol] = h
	}
	for _, m := range p.Marks {
		h, ok := holdings[m.Symbol]
		if !ok || h.Quantity != m.Quantity || m.Price <= 0 || math.IsNaN(m.Price) || math.IsInf(m.Price, 0) || math.Abs(h.MarketValue-m.Price*float64(m.Quantity)) > 0.02 || m.BarStart.IsZero() || m.BarStart.Second() != 0 || m.BarStart.Nanosecond() != 0 || !m.BarEnd.Equal(m.BarStart.Add(time.Minute)) || m.BarEnd.After(s.AsOf) {
			return fmt.Errorf("account snapshot: invalid holding valuation evidence")
		}
		session, err := tradecal.Default.SessionAt(m.BarStart)
		if err != nil || !session.Trading {
			return fmt.Errorf("account snapshot: non-trading valuation bar")
		}
		delete(holdings, m.Symbol)
	}
	if p.Complete && len(holdings) != 0 {
		return fmt.Errorf("account snapshot: incomplete holding valuation coverage")
	}
	return nil
}

func (s Snapshot) Fresh(now time.Time, maxAge time.Duration) bool {
	return !s.AsOf.IsZero() && !s.AsOf.After(now.Add(5*time.Second)) && now.Sub(s.AsOf) <= maxAge && s.TradingDay == now.In(tradecal.Shanghai()).Format("2006-01-02")
}

func (s Snapshot) Ready(now time.Time, maxAge time.Duration) bool {
	return s.Fresh(now, maxAge) && s.DayStartKnown && s.Reconciliation.Status == "matched" && now.Sub(s.Reconciliation.AsOf) <= maxAge
}
