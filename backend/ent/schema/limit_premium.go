package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// LimitPremium 昨日涨停今日溢价的跨日快照（监控业务监控方案 §7 P2）。
// 每个交易日一行：trade_date 为溢价兑现日（今日），base_date 为前一交易日（涨停日）。
// 09:26 的 market.premium.open 任务先写 open_premium（集合竞价涨幅口径），
// 16:45 的 market.premium.close 任务补齐当日 OHLC 与 close_premium / high_premium。
// 溢价率均为分数（0.0323 = 3.23%），分母 pre_close 取 base_date 收盘价。
type LimitPremium struct {
	ent.Schema
}

func (LimitPremium) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "limit_premium"}}
}

func (LimitPremium) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Time("trade_date").SchemaType(pgDate).Comment("溢价兑现日（今日）"),
		field.Time("base_date").SchemaType(pgDate).Comment("涨停日（前一交易日）"),
		field.String("name").MaxLen(64).Default(""),
		field.Int("consecutive").Default(1).Comment("base_date 当日连板数"),
		field.Float("pre_close").Optional().Nillable().SchemaType(numericPrice).Comment("base_date 收盘价（分母）"),
		field.Float("open").Optional().Nillable().SchemaType(numericPrice),
		field.Float("high").Optional().Nillable().SchemaType(numericPrice),
		field.Float("close").Optional().Nillable().SchemaType(numericPrice),
		field.Float("open_premium").Optional().Nillable().Comment("open/pre_close-1，竞价口径先落"),
		field.Float("close_premium").Optional().Nillable().Comment("close/pre_close-1"),
		field.Float("high_premium").Optional().Nillable().Comment("high/pre_close-1"),
		field.Bool("limit_again").Default(false).Comment("今日收盘再度涨停"),
		field.String("source").MaxLen(16).Default(""),
		field.Time("as_of").SchemaType(pgTimestamptz),
	}
}

func (LimitPremium) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trade_date", "symbol").Unique(),
		index.Fields("base_date"),
		index.Fields("trade_date", "limit_again"),
	}
}
