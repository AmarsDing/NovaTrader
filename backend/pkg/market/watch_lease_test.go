package market

import (
	"testing"

	"github.com/google/uuid"
)

func TestTradeWatchLeaseBoundsAndCanonicalIdentity(t *testing.T) {
	base := TradeWatchLease{Version: 1, Owner: "trade." + uuid.NewString(), Symbols: []string{"600519.SH", "000001.SZ"}, TTLSeconds: 120}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*TradeWatchLease){
		func(l *TradeWatchLease) { l.Version = 0 },
		func(l *TradeWatchLease) { l.Owner = "trade.not-a-process-id" },
		func(l *TradeWatchLease) { l.TTLSeconds = 3600 },
		func(l *TradeWatchLease) { l.Symbols = []string{"600519.SH", "600519.SH"} },
		func(l *TradeWatchLease) { l.Symbols = []string{"SHSE.600519"} },
		func(l *TradeWatchLease) { l.Symbols = make([]string, MaxTradeWatchSymbols+1) },
	} {
		lease := base
		mutate(&lease)
		if lease.Validate() == nil {
			t.Fatalf("invalid lease accepted: %+v", lease)
		}
	}
}
