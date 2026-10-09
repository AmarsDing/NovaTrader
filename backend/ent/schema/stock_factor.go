package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// StockFactor 个股因子截面，对应 M02 FR-02-09。as_of 是数据截至时刻，回测只读 as_of ≤ 当前时刻的行。
// kind：close 收盘、intraday 盘中检查点、auction 竞价。factors 的键见 M02 设计文档第 4、7 节。
type StockFactor struct {
	ent.Schema
}

func (StockFactor) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "stock_factor"}}
}

func (StockFactor) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Time("trade_date").SchemaType(pgDate),
		field.String("kind").MaxLen(16).NotEmpty(),
		field.Time("as_of").SchemaType(pgTimestamptz),
		field.JSON("factors", map[string]float64{}),
		field.Bool("stale").Default(false).Comment("计算时快照已过期"),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(pgTimestamptz),
	}
}

func (StockFactor) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("symbol", "trade_date", "kind", "as_of").Unique(),
		index.Fields("trade_date", "kind", "as_of"),
	}
}
