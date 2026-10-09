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

// StrategyFeedback 信号事后反馈。对齐 doc/NovaTrader智脑方案.md 4.3.1。
// false_negative 标记规则已通过但没有入选、事后证明错过的标的。
type StrategyFeedback struct {
	ent.Schema
}

func (StrategyFeedback) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "strategy_feedback"}}
}

func (StrategyFeedback) Fields() []ent.Field {
	return []ent.Field{
		field.Time("trade_date").
			SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.String("stock_code").MaxLen(16).NotEmpty(),
		field.Int("signal_id").Optional().Nillable(),
		field.Float("predicted_return").
			SchemaType(map[string]string{dialect.Postgres: "numeric(12,6)"}),
		field.Float("actual_return").
			SchemaType(map[string]string{dialect.Postgres: "numeric(12,6)"}),
		field.Bool("signal_correct").Optional().Nillable(),
		field.Bool("false_negative").Default(false),
		field.Text("reasoning").Optional().Nillable(),
		field.Text("error_analysis").Optional().Nillable(),
		field.String("attribution").MaxLen(32).Default("").
			Comment("M07 归因：indicator_failed / news_misread / unfilled / emotion_mismatch / missed；空为未归因"),
		field.String("strategy").MaxLen(64).Default(""),
		field.Time("update_time").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (StrategyFeedback) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trade_date"),
		index.Fields("stock_code"),
	}
}
