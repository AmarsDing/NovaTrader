package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// LimitPool 数据源给出的涨停、跌停、炸板池，由 M01 写入。
// 与 M02 自己从快照算出的 limit_board 互为核对；涨停原因、所属题材只有这里有。
type LimitPool struct {
	ent.Schema
}

func (LimitPool) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "limit_pool"}}
}

func (LimitPool) Fields() []ent.Field {
	return []ent.Field{
		field.Time("trade_date").SchemaType(pgDate),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.String("pool").MaxLen(8).NotEmpty().Comment("up 涨停 / down 跌停 / broken 炸板"),
		field.String("name").MaxLen(32).Default(""),
		field.Float("close").Optional().Nillable().SchemaType(numericPrice),
		field.Float("pct_chg").Optional().Nillable().Comment("%"),
		field.Float("amount").Optional().Nillable().SchemaType(numericAmount).Comment("成交额，元"),
		field.Time("first_seal_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Time("last_seal_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Int("open_count").Default(0).Comment("炸板次数"),
		field.Float("seal_amount").Default(0).SchemaType(numericMoney).Comment("封单额，元"),
		field.Int("consecutive").Default(0).Comment("连板数"),
		field.String("reason").MaxLen(128).Default("").Comment("涨停原因或所属行业"),
		field.String("source").MaxLen(16).Default(""),
		field.Time("as_of").Default(time.Now).SchemaType(pgTimestamptz),
	}
}

func (LimitPool) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trade_date", "symbol", "pool").Unique(),
		index.Fields("trade_date", "pool"),
	}
}
