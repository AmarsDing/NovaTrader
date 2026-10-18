package biz

import (
	"context"
	"fmt"
	"sort"

	"server/pkg/market"
)

// SimulationWatchSymbols does not depend on an open desktop panel. Holding
// marks and all working SIM orders keep their minute stream registered.
func (e *Engine) SimulationWatchSymbols(ctx context.Context) (out []string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	err = e.withLedger(ctx, "global", func(c context.Context) error {
		positions, err := e.store.ListPositions(c, BookPaper)
		if err != nil {
			return err
		}
		orders, err := e.store.ListOpen(c, BookPaper, "")
		if err != nil {
			return err
		}
		set := make(map[string]struct{}, len(positions)+len(orders))
		for _, p := range positions {
			if p.Quantity > 0 {
				set[p.Symbol] = struct{}{}
			}
		}
		for _, o := range orders {
			set[o.Symbol] = struct{}{}
		}
		if len(set) > market.MaxTradeWatchSymbols {
			return fmt.Errorf("SIM watch universe exceeds %d; stream registration blocked", market.MaxTradeWatchSymbols)
		}
		out = make([]string, 0, len(set))
		for s := range set {
			out = append(out, s)
		}
		sort.Strings(out)
		return nil
	})
	return
}
