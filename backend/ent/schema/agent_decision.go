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

// AgentDecision 模型调用与研判审计。对齐 sql/init_db.sql。模型不下单，这里只留痕。
// trace_id 之后的字段由 M05 追加；content 存事实包和结论，追问时取回。
type AgentDecision struct {
	ent.Schema
}

func (AgentDecision) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "agent_decisions"}}
}

func (AgentDecision) Fields() []ent.Field {
	return []ent.Field{
		field.String("agent_name").MaxLen(50).Optional().Nillable(),
		field.String("decision_type").MaxLen(50).Optional().Nillable(),
		field.JSON("content", map[string]any{}).Optional(),
		field.Float("confidence").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "numeric(8,4)"}),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("trace_id").MaxLen(64).Default(""),
		field.String("symbol").MaxLen(16).Default(""),
		field.String("prompt_version").MaxLen(64).Default(""),
		field.String("model").MaxLen(128).Default(""),
		field.Bool("ai_degraded").Default(false),
		field.Bool("discarded").Default(false),
		field.Int("latency_ms").Default(0),
	}
}

func (AgentDecision) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at"),
		index.Fields("symbol", "created_at"),
		index.Fields("trace_id"),
	}
}
