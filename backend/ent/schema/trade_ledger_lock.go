package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// TradeLedgerLock serializes SIM/LIVE ledger mutations across trade instances
// with a PostgreSQL row lock held through the ledger/outbox transaction.
type TradeLedgerLock struct{ ent.Schema }

func (TradeLedgerLock) Fields() []ent.Field {
	return []ent.Field{field.String("book").MaxLen(16).NotEmpty().Unique()}
}
