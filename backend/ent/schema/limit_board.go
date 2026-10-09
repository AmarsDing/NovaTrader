package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// LimitBoard 涨跌停生态，对应 M02 FR-02-03。每只股票每天每个方向一行，盘中持续更新，收盘定稿。
// status：SEALED 封住、BROKEN 曾触板未封住。连板数只对收盘 SEALED 的涨停行有意义。
type LimitBoard struct {
	ent.Schema
}

func (LimitBoard) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "limit_board"}}
}

func (LimitBoard) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Time("trade_date").SchemaType(pgDate),
		field.String("direction").MaxLen(8).NotEmpty().Comment("UP / DOWN"),
		field.String("status").MaxLen(16).NotEmpty().Comment("SEALED / BROKEN"),
		field.Float("limit_price").SchemaType(numericPrice),
		field.Time("first_seal_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Time("last_seal_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Int("open_count").Default(0).Comment("炸板次数：从封住到打开的次数"),
		field.Float("seal_amount").Default(0).SchemaType(numericMoney).Comment("最近一次封板时的封单额，元"),
		field.Int("consecutive").Default(0).Comment("连板数"),
		field.Time("as_of").SchemaType(pgTimestamptz),
	}
}

func (LimitBoard) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("symbol", "trade_date", "direction").Unique(),
		index.Fields("trade_date", "direction", "status"),
	}
}
