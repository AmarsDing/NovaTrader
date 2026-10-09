package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// HsgtTop10 沪深股通十大成交股。2024-08-19 起交易所不再披露北向每日净买入，
// net_amount 只在源头仍给出时才有值。
type HsgtTop10 struct {
	ent.Schema
}

func (HsgtTop10) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "hsgt_top10"}}
}

func (HsgtTop10) Fields() []ent.Field {
	return []ent.Field{
		field.Time("trade_date").SchemaType(pgDate),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.String("channel").MaxLen(8).NotEmpty().Comment("SH 沪股通 / SZ 深股通"),
		field.Int("rank").Default(0),
		field.String("name").MaxLen(32).Default(""),
		field.Float("close").Optional().Nillable().SchemaType(numericPrice),
		field.Float("pct_chg").Optional().Nillable().Comment("%"),
		field.Float("amount").Default(0).SchemaType(numericAmount).Comment("成交额，元"),
		field.Float("net_amount").Optional().Nillable().SchemaType(numericAmount).Comment("净买入，元"),
		field.String("source").MaxLen(16).Default(""),
	}
}

func (HsgtTop10) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trade_date", "symbol", "channel").Unique(),
	}
}
