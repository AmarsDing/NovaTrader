package market

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"server/pkg/symbol"
)

const WatchLeaseVersion = 1
const WatchLeaseSeconds = 120
const MaxTradeWatchSymbols = 1000

// TradeWatchLease is a bounded process lease, not an order or an execution
// authorization. Symbols are rebuilt from the durable SIM ledger every renewal.
type TradeWatchLease struct {
	Version    int      `json:"version"`
	Owner      string   `json:"owner"`
	Symbols    []string `json:"symbols"`
	TTLSeconds int      `json:"ttl_seconds"`
}

func (l TradeWatchLease) Validate() error {
	if l.Version != WatchLeaseVersion || l.TTLSeconds != WatchLeaseSeconds || len(l.Symbols) > MaxTradeWatchSymbols || !strings.HasPrefix(l.Owner, "trade.") {
		return fmt.Errorf("invalid trade watch lease")
	}
	id, err := uuid.Parse(strings.TrimPrefix(l.Owner, "trade."))
	if err != nil || l.Owner != "trade."+id.String() {
		return fmt.Errorf("invalid trade watch owner")
	}
	seen := make(map[string]bool, len(l.Symbols))
	for _, raw := range l.Symbols {
		s, err := symbol.Parse(raw)
		if err != nil || s.Tongdaxin() != raw || seen[raw] {
			return fmt.Errorf("invalid trade watch symbols")
		}
		seen[raw] = true
	}
	return nil
}
