package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BacktestReport 回测报告，一个任务一行。汇总列供列表和准入检查查询，result 是完整报告 JSON（pkg/backtest.Report）。
type BacktestReport struct {
	ent.Schema
}

func (BacktestReport) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "backtest_report"}}
}

func (BacktestReport) Fields() []ent.Field {
	return []ent.Field{
		field.Int("task_id"),
		field.Float("total_return").Default(0).SchemaType(numericRatio),
		field.Float("annual_return").Default(0).SchemaType(numericRatio),
		field.Float("max_drawdown").Default(0).SchemaType(numericRatio),
		field.Float("sharpe").Default(0).SchemaType(numericRatio),
		field.Float("win_rate").Default(0).SchemaType(numericRatio),
		field.Float("profit_factor").Default(0).SchemaType(numericRatio),
		field.Int("trade_count").Default(0).Comment("回合数"),
		field.Bool("lookahead_pass").Optional().Nillable().Comment("没做截断自检为空"),
		field.Bool("oos_decay").Default(false).Comment("参数搜索或前推标了样本外衰减"),
		field.String("fills_hash").MaxLen(64).Default(""),
		field.JSON("result", json.RawMessage{}),
		field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz),
	}
}

func (BacktestReport) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("task_id").Unique(),
	}
}
