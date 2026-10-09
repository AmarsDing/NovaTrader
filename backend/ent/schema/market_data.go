package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MarketData 不复权 K 线。由 sql/init_db.sql 的日线表扩展为日线与 1 分钟线，对应 M02 FR-02-01。
// 价格单位是元，成交量是股，成交额是元。复权在查询时用 adj_factor 计算。
type MarketData struct {
	ent.Schema
}

func (MarketData) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "market_data"}}
}

func (MarketData) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty().Comment("600519.SH"),
		field.Time("bar_time").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).
			Comment("K 线开始时间"),
		field.String("freq").MaxLen(8).NotEmpty().Comment("1m 分钟，1d 日线"),
		field.Float("open").Optional().Nillable().SchemaType(numericPrice),
		field.Float("high").Optional().Nillable().SchemaType(numericPrice),
		field.Float("low").Optional().Nillable().SchemaType(numericPrice),
		field.Float("close").Optional().Nillable().SchemaType(numericPrice),
		field.Int64("volume").Optional().Nillable().Comment("成交量，股"),
		field.Float("amount").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "numeric(20,4)"}).
			Comment("成交额，元"),
		field.Float("adj_factor").Default(1).
			SchemaType(map[string]string{dialect.Postgres: "numeric(18,8)"}).
			Comment("后复权因子，不复权价格本身不改写"),
		field.Float("pre_close").Optional().Nillable().SchemaType(numericPrice).
			Comment("交易所前收盘，除权日不等于昨收"),
		field.String("source").MaxLen(16).Default("").
			Comment("market_agg 为本服务由分钟线合成，其他为 M01 数据源名"),
	}
}

func (MarketData) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("symbol", "bar_time", "freq").Unique(),
		index.Fields("bar_time"),
	}
}
