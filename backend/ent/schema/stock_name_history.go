package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// StockNameHistory 证券简称变更史。回测按交易日取当时的名称判定 ST（M07 设计文档第 2 节），由 M01 采集写入。
// end_date 为空表示至今。
type StockNameHistory struct {
	ent.Schema
}

func (StockNameHistory) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "stock_name_history"}}
}

func (StockNameHistory) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty().Comment("600519.SH"),
		field.String("name").MaxLen(32).NotEmpty(),
		field.Time("start_date").SchemaType(pgDate),
		field.Time("end_date").Optional().Nillable().SchemaType(pgDate),
		field.String("source").MaxLen(32).Default(""),
		field.Time("update_time").Default(time.Now).UpdateDefault(time.Now).SchemaType(pgTimestamptz),
	}
}

func (StockNameHistory) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("symbol", "start_date").Unique(),
	}
}
