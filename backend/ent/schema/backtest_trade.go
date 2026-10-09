package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BacktestTrade 回测成交明细，一个任务一组，任务结束时整批写入。side 为 buy / sell / dividend（除权等值入账）。
type BacktestTrade struct {
	ent.Schema
}

func (BacktestTrade) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "backtest_trade"}}
}

func (BacktestTrade) Fields() []ent.Field {
	return []ent.Field{
		field.Int("task_id"),
		field.Int("seq"),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.String("side").MaxLen(8).NotEmpty(),
		field.Time("trade_time").SchemaType(pgTimestamptz),
		field.Float("price").SchemaType(numericPrice),
		field.Int("quantity"),
		field.Float("amount").SchemaType(numericMoney),
		field.Float("fee").Default(0).SchemaType(numericMoney),
		field.String("reason").MaxLen(256).Default(""),
		field.Float("pnl").Default(0).SchemaType(numericMoney).Comment("回合结束的那笔卖出填回合盈亏"),
		field.Int("round_id").Default(0),
		field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz),
	}
}

func (BacktestTrade) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("task_id", "seq").Unique(),
	}
}
