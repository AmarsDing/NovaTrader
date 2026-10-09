package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MarginDetail 个股融资融券明细，由 M01 盘后写入。金额单位元，融券卖出量单位股。
type MarginDetail struct {
	ent.Schema
}

func (MarginDetail) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "margin_detail"}}
}

func (MarginDetail) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Time("trade_date").SchemaType(pgDate),
		field.Float("rzye").Default(0).SchemaType(numericAmount).Comment("融资余额"),
		field.Float("rzmre").Default(0).SchemaType(numericAmount).Comment("融资买入额"),
		field.Float("rzche").Default(0).SchemaType(numericAmount).Comment("融资偿还额"),
		field.Float("rqye").Default(0).SchemaType(numericAmount).Comment("融券余额"),
		field.Int64("rqmcl").Default(0).Comment("融券卖出量，股"),
		field.Float("rzrqye").Default(0).SchemaType(numericAmount).Comment("融资融券余额"),
		field.String("source").MaxLen(16).Default(""),
	}
}

func (MarginDetail) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("symbol", "trade_date").Unique(),
		index.Fields("trade_date"),
	}
}
