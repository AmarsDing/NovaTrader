package biz

import "context"

type memLedgerKey struct{}

func (m *MemStore) InLedger(ctx context.Context) bool { return ctx.Value(memLedgerKey{}) == m }
func (m *MemStore) FinishIntentDetached(ctx context.Context, id, state, reason string) error {
	return m.FinishIntent(ctx, id, state, reason)
}

func (m *MemStore) WithLedger(ctx context.Context, _ string, fn func(context.Context) error) error {
	if ctx.Value(memLedgerKey{}) == m {
		return fn(ctx)
	}
	m.ledgerMu.Lock()
	defer m.ledgerMu.Unlock()
	return fn(context.WithValue(ctx, memLedgerKey{}, m))
}
func (m *MemStore) ClaimIntent(_ context.Context, in Intent) (Intent, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.intents {
		if old.BusinessKey == in.BusinessKey || old.ClientOrderID == in.ClientOrderID {
			return old, false, nil
		}
	}
	m.intents[in.ClientOrderID] = in
	return in, true, nil
}
func (m *MemStore) FinishIntent(_ context.Context, id, state, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.intents[id]
	if !ok {
		return ErrNotFound
	}
	old.State = state
	old.Reason = reason
	m.intents[id] = old
	return nil
}
