package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Introspection 每日自省。M07 设计文档第 12.1 节：每个交易日每个 book 一行，重复执行覆盖。
// attribution 是错误归因计数，键为 indicator_failed / news_misread / unfilled / emotion_mismatch / unknown。
type Introspection struct {
	ent.Schema
}

func (Introspection) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "introspection"}}
}

func (Introspection) Fields() []ent.Field {
	return []ent.Field{
		field.Time("trade_date").SchemaType(pgDate),
		field.String("book").MaxLen(16).Default("paper"),
		field.Int("window_days").Default(30),
		field.Int("sample_count").Default(0),
		field.Float("win_rate").Default(0).SchemaType(numericRatio),
		field.Float("avg_return").Default(0).SchemaType(numericRatio),
		field.Float("max_drawdown").Default(0).SchemaType(numericRatio),
		field.Int("missed_count").Default(0),
		field.JSON("attribution", map[string]int{}).Optional(),
		field.Bool("triggered").Default(false),
		field.String("trigger_reason").MaxLen(256).Default(""),
		field.Int("task_id").Optional().Nillable().Comment("触发进化时的 evolve 任务"),
		field.Text("note").Default(""),
		field.Time("created_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(pgTimestamptz),
	}
}

func (Introspection) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trade_date", "book").Unique(),
	}
}
