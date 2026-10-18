package core

import (
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
	"server/pkg/events"
	"server/pkg/market"
)

func (w *Worker) onWatchlist(msg *nats.Msg) {
	// No raw payload is logged. The lease is not an order and grants no trading
	// permission; it only requests an existing market stream on the private bus.
	if len(msg.Data) > 48*1024 {
		return
	}
	env, err := events.Unmarshal(msg.Data)
	if err != nil || env.Subject != events.SubjectMarketWatchlist || env.Source != "trade" {
		return
	}
	var lease market.TradeWatchLease
	if err := json.Unmarshal(env.Payload, &lease); err != nil || lease.Validate() != nil {
		return
	}
	w.uc.Watch().Set(lease.Owner, lease.Symbols, time.Duration(lease.TTLSeconds)*time.Second, time.Now())
}
