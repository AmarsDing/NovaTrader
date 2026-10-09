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

// TradeOrder 委托。模拟盘和实盘共用状态机，client_order_id 全局唯一。
type TradeOrder struct {
	ent.Schema
}

func (TradeOrder) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "trade_order"}}
}

func (TradeOrder) Fields() []ent.Field {
	return []ent.Field{
		field.String("client_order_id").MaxLen(64).NotEmpty().Unique(),
		field.String("book").MaxLen(16).Default("paper").Comment("paper 模拟盘，live 实盘"),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.String("side").MaxLen(8).NotEmpty().Comment("buy / sell"),
		field.String("status").MaxLen(16).NotEmpty(),
		field.Float("price").SchemaType(numericPrice),
		field.Int("volume").Default(0),
		field.Int("filled").Default(0),
		field.String("source").MaxLen(16).Default("auto").Comment("auto / manual"),
		field.String("operator").MaxLen(64).Default(""),
		field.String("signal_id").MaxLen(64).Default(""),
		field.String("strategy_version").MaxLen(64).Default(""),
		field.String("reason").MaxLen(200).Default(""),
		field.String("channel").MaxLen(32).Default("sim"),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (TradeOrder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("book", "status"),
		index.Fields("book", "symbol", "status"),
	}
}
