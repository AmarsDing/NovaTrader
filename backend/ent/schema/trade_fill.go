package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TradeFill 成交。一笔委托可以有多笔成交，费用按本笔计算。
type TradeFill struct {
	ent.Schema
}

func (TradeFill) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "trade_fill"}}
}

func (TradeFill) Fields() []ent.Field {
	return []ent.Field{
		field.String("client_order_id").MaxLen(64).NotEmpty(),
		field.String("book").MaxLen(16).Default("paper"),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.String("side").MaxLen(8).NotEmpty(),
		field.Int("qty"),
		field.Float("price").SchemaType(numericPrice),
		field.Float("amount").SchemaType(numericMoney),
		field.Float("fee").SchemaType(numericMoney),
		field.String("signal_id").MaxLen(64).Default(""),
		field.String("strategy_version").MaxLen(64).Default(""),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (TradeFill) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("client_order_id"),
		index.Fields("book", "created_at"),
	}
}
