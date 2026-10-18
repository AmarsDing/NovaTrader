package biz

import (
	"context"
	"math"
	"time"
)

func (m *MemStore) LastConsumedSimBar(ctx context.Context, book, symbol string) (time.Time, error) {
	if !m.InLedger(ctx) || book != BookPaper {
		return time.Time{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.simBars[book+"|"+symbol], nil
}

func (m *MemStore) AdvanceSimBar(ctx context.Context, book, symbol string, at time.Time, close float64) error {
	if !m.InLedger(ctx) || book != BookPaper || at.IsZero() || close <= 0 || math.IsNaN(close) || math.IsInf(close, 0) {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := book + "|" + symbol
	if !at.After(m.simBars[key]) {
		return ErrConflict
	}
	m.simBars[key] = at
	return nil
}
