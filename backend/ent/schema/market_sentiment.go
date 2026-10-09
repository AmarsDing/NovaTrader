package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MarketSentiment 全市场情绪截面，对应 M02 FR-02-04。phase 给 M06 评分系数、M08 仓位系数。
// 口径见 M02 需求文档第 2 节，阶段阈值见设计文档第 6 节。
type MarketSentiment struct {
	ent.Schema
}

func (MarketSentiment) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "market_sentiment"}}
}

func (MarketSentiment) Fields() []ent.Field {
	return []ent.Field{
		field.Time("trade_date").SchemaType(pgDate),
		field.Time("as_of").SchemaType(pgTimestamptz),
		field.Int("up_count").Default(0),
		field.Int("down_count").Default(0),
		field.Int("broken_count").Default(0),
		field.Float("broken_rate").Default(0),
		field.Int("max_height").Default(0),
		field.Float("profit_effect").Default(0).Comment("昨日涨停今日平均涨幅，%"),
		field.Int("advance").Default(0),
		field.Int("decline").Default(0),
		field.Int("flat").Default(0),
		field.Float("amount").Default(0).SchemaType(numericAmount),
		field.String("phase").MaxLen(16).NotEmpty(),
		field.Float("score_coef").Default(1),
		field.Float("position_scale").Default(1),
		field.Bool("stale").Default(false),
	}
}

func (MarketSentiment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trade_date", "as_of").Unique(),
	}
}
