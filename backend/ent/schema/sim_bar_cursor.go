package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SimBarCursor stores the last consumed source minute and validated close per SIM symbol.
// It advances in the same global-ledger transaction as fills, positions and
// outbox, preventing duplicate/corrected/replayed minute liquidity after restart.
type SimBarCursor struct{ ent.Schema }

func (SimBarCursor) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "sim_bar_cursor"}}
}

func (SimBarCursor) Fields() []ent.Field {
	return []ent.Field{
		field.String("book").MaxLen(16).NotEmpty().Immutable(),
		field.String("symbol").MaxLen(16).NotEmpty().Immutable(),
		field.Time("last_bar_start").SchemaType(pgTimestamptz),
		field.Float("last_close").Default(0).SchemaType(numericPrice).Comment("Validated source-minute close; zero on legacy cursors has no valuation proof"),
		field.Int("valuation_version").Default(0).Comment("1 only after a validated runtime minute bar supplies last_close"),
	}
}

func (SimBarCursor) Indexes() []ent.Index {
	return []ent.Index{index.Fields("book", "symbol").Unique()}
}
