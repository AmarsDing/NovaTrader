package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OverseasQuote 外围收盘行情（美股、中概、A50、汇率、商品），由 M01 经 AkShare 写入。trade_date 为当地交易日。
type OverseasQuote struct {
	ent.Schema
}

func (OverseasQuote) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "overseas_quote"}}
}

func (OverseasQuote) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").MaxLen(32).NotEmpty(),
		field.String("name").MaxLen(64).Default(""),
		field.Time("trade_date").SchemaType(pgDate),
		field.Float("close").Optional().Nillable(),
		field.Float("pct_chg").Default(0).Comment("%"),
		field.Time("as_of").SchemaType(pgTimestamptz).Comment("收盘数据可得时刻（北京时间）"),
		field.String("source").MaxLen(16).Default(""),
	}
}

func (OverseasQuote) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("code", "trade_date").Unique(),
		index.Fields("as_of"),
	}
}

// OverseasMapping 外围标的到 A 股板块的映射，人工维护，对应 M02 FR-02-07。
type OverseasMapping struct {
	ent.Schema
}

func (OverseasMapping) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "overseas_mapping"}}
}

func (OverseasMapping) Fields() []ent.Field {
	return []ent.Field{
		field.String("asset_code").MaxLen(32).NotEmpty(),
		field.String("sector_code").MaxLen(32).NotEmpty(),
		field.Float("weight").Default(1),
		field.String("note").MaxLen(128).Default(""),
	}
}

func (OverseasMapping) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("asset_code", "sector_code").Unique(),
	}
}
