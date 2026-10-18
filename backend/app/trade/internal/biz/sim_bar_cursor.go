package biz

import (
	"context"
	"time"
)

// SimBarStore is used only while the global ledger lock is held. Cursor writes
// must share the transaction that commits the matching ledger and outbox.
type SimBarStore interface {
	LastConsumedSimBar(context.Context, string, string) (time.Time, error)
	AdvanceSimBar(context.Context, string, string, time.Time, float64) error
}
