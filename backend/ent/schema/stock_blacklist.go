package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// StockBlacklist 选股黑名单。M06 FR-06-01：命中的股票在硬过滤阶段淘汰。expires_at 为空表示永久。
type StockBlacklist struct {
	ent.Schema
}

func (StockBlacklist) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "stock_blacklist"}}
}

func (StockBlacklist) Fields() []ent.Field {
	return []ent.Field{
		field.String("stock_code").MaxLen(16).NotEmpty(),
		field.Text("reason").Default(""),
		field.Time("expires_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.String("created_by").MaxLen(32).Default(""),
		field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz),
	}
}

func (StockBlacklist) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("stock_code").Unique(),
	}
}
