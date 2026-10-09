package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SignalEvent 信号状态变化流水。M06 设计文档第 7 节：每次改 trade_signals.status 都在同一事务里追加一行。
// from_status 为空表示信号刚创建。
type SignalEvent struct {
	ent.Schema
}

func (SignalEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "signal_events"}}
}

func (SignalEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int("signal_id"),
		field.String("from_status").MaxLen(16).Default(""),
		field.String("to_status").MaxLen(16).NotEmpty(),
		field.Text("reason").Default(""),
		field.String("actor").MaxLen(32).Default("").Comment("strategy / risk / trade / user"),
		field.String("trace_id").MaxLen(64).Default(""),
		field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz),
	}
}

func (SignalEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("signal_id", "created_at"),
	}
}
