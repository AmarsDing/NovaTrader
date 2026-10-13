package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MarketBreadth 市场广度快照（监控业务监控方案 §7 P2）：20 日均线上占比。
// 每个交易日一行：对全市场当日有日线（freq=1d，bar_time=trade_date）且近 window 根收盘齐全的股票，
// 统计收盘价高于 20 日均线的占比。market.breadth 任务收盘后落库，只读接口读表。
type MarketBreadth struct {
	ent.Schema
}

func (MarketBreadth) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "market_breadth"}}
}

func (MarketBreadth) Fields() []ent.Field {
	return []ent.Field{
		field.Time("trade_date").SchemaType(pgDate),
		field.Time("as_of").SchemaType(pgTimestamptz),
		field.Int("window").Default(20).Comment("均线窗口"),
		field.Int("above").Default(0).Comment("收盘价高于均线只数"),
		field.Int("below").Default(0).Comment("收盘价低于均线只数"),
		field.Int("flat").Default(0).Comment("收盘价等于均线只数"),
		field.Int("total").Default(0).Comment("样本总数（近 window 根收盘齐全）"),
		field.Float("ratio").Default(0).Comment("above/total"),
		field.String("source").MaxLen(16).Default(""),
	}
}

func (MarketBreadth) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trade_date").Unique(),
	}
}
