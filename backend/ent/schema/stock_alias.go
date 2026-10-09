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

// StockAlias 人工维护的证券别名：曾用名、英文名、俗称。证券简称和全称直接取 stock_basic，不在这里重复。
// M03 关联词典 = stock_basic + 本表。
type StockAlias struct {
	ent.Schema
}

func (StockAlias) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "stock_alias"}}
}

func (StockAlias) Fields() []ent.Field {
	return []ent.Field{
		field.String("alias").MaxLen(64).NotEmpty(),
		field.String("stock_code").MaxLen(16).NotEmpty().Comment("600519.SH"),
		field.String("kind").MaxLen(16).Default("manual").Comment("former / english / nickname / manual"),
		field.Float("confidence").Default(0.8).
			SchemaType(map[string]string{dialect.Postgres: "numeric(5,4)"}),
		field.Bool("enabled").Default(true),
		field.Time("update_time").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (StockAlias) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("alias", "stock_code").Unique(),
		index.Fields("stock_code"),
	}
}
