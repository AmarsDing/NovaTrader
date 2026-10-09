package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AccountSnapshot 账户快照。对齐 doc/NovaTrader智脑方案.md 4.1.3。
type AccountSnapshot struct {
	ent.Schema
}

func (AccountSnapshot) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "account_snapshots"}}
}

func (AccountSnapshot) Fields() []ent.Field {
	return []ent.Field{
		field.String("book").MaxLen(16).Default("paper"),
		field.Time("snapshot_time").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Float("total_equity").SchemaType(numericMoney),
		field.Float("available_funds").SchemaType(numericMoney),
		field.Float("unrealized_pnl").SchemaType(numericMoney),
		field.Float("realized_pnl").SchemaType(numericMoney),
		field.Float("margin_used").Default(0).SchemaType(numericMoney),
		field.Float("risk_ratio").
			SchemaType(map[string]string{dialect.Postgres: "numeric(8,4)"}),
		field.Int("position_count"),
	}
}

func (AccountSnapshot) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("book", "snapshot_time"),
	}
}
