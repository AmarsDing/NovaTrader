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

// StockBasic 股票基础信息。对齐 doc/NovaTrader智脑方案.md 4.1.1。
// 停牌、ST、行业、上市日期给涨跌停幅度和能否交易使用。
type StockBasic struct {
	ent.Schema
}

func (StockBasic) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "stock_basic"}}
}

func (StockBasic) Fields() []ent.Field {
	return []ent.Field{
		field.String("stock_code").MaxLen(16).NotEmpty().Immutable().
			Comment("证券代码，统一为 600519.SH"),
		field.String("stock_name").MaxLen(32).NotEmpty(),
		field.String("market").MaxLen(8).NotEmpty().Comment("SH / SZ / BJ"),
		field.Time("list_date").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.Time("delist_date").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.String("industry").MaxLen(64).Optional().Nillable(),
		field.Text("concept").Optional().Nillable().Comment("概念板块，逗号分隔"),
		field.Int64("total_share").Optional().Nillable(),
		field.Int64("free_share").Optional().Nillable(),
		field.Int64("float_share").Optional().Nillable().Comment("流通股本，股；换手按通达信口径用它"),
		field.Float("circ_mv").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "numeric(18,2)"}).
			Comment("流通市值，元"),
		field.Float("pe_ttm").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "numeric(12,4)"}),
		field.Float("pb").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "numeric(12,4)"}),
		field.Bool("st_flag").Default(false),
		field.Bool("suspend_flag").Default(false),
		field.Time("update_time").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (StockBasic) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("stock_code").Unique(),
		index.Fields("market"),
		index.Fields("industry"),
	}
}
