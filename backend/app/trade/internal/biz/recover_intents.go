package biz

import "context"

// RecoverIntents reconciles by the original ID only. No age/timeout is proof
// that an external submission failed, and this method never submits an order.
func (e *Engine) RecoverIntents(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.withLedger(ctx, "global", e.recoverIntents)
}

func (e *Engine) recoverIntents(ctx context.Context) error {
	store, err := e.automationStore()
	if err != nil {
		return err
	}
	durable := e.store.(LedgerStore)
	for _, book := range []string{BookPaper, BookLive} {
		rows, err := store.ListUnresolvedIntents(ctx, book)
		if err != nil {
			return err
		}
		for _, i := range rows {
			if i.State != "claimed" && i.State != "unknown" {
				continue
			}
			o, found, err := e.store.LoadOrder(ctx, i.ClientOrderID)
			if err != nil {
				return err
			}
			state, reason := "unknown", "未找到可证明结果的原订单；保留意图，等待核对"
			if found {
				state = o.Status
				reason = o.Reason
			}
			if state != i.State || reason != i.Reason {
				if err = durable.FinishIntent(ctx, i.ClientOrderID, state, reason); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
