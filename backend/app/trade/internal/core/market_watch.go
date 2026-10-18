package core

import (
	"context"
	"time"

	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/observe"
)

// NATS market.* is intentionally transient. Renewing from the durable ledger
// every five seconds recovers a lost command or a restarted market process.
func (r *Runner) watchLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	refresh := func() {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := r.renewWatch(c); err != nil && ctx.Err() == nil {
			r.log.Warnf("SIM market watch registration: %v", err)
		}
	}
	refresh()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

func (r *Runner) renewWatch(ctx context.Context) (err error) {
	finish := observe.ForService("trade").Op("market_lease").Begin()
	defer func() { observe.FinishError(finish, err) }()
	symbols, err := r.e.SimulationWatchSymbols(ctx)
	if err != nil {
		return err
	}
	lease := market.TradeWatchLease{Version: market.WatchLeaseVersion, Owner: r.watchOwner, Symbols: symbols, TTLSeconds: market.WatchLeaseSeconds}
	if err := lease.Validate(); err != nil {
		return err
	}
	env, err := events.NewWithContext(ctx, "trade", events.SubjectMarketWatchlist, lease)
	if err != nil {
		return err
	}
	return r.bus.Publish(ctx, env)
}
