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

// LlmCallLog 每次模型调用一行，重试也单独一行。对应 M05 FR-05-08。
// status：ok 合格，invalid 输出不合格，unavailable 排队满/超时/熔断，error 其他错误。
type LlmCallLog struct {
	ent.Schema
}

func (LlmCallLog) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "llm_call_log"}}
}

func (LlmCallLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("trace_id").MaxLen(64).Default(""),
		field.String("task").MaxLen(32).NotEmpty().Comment("technical、news、briefing 等"),
		field.String("tier").MaxLen(16).NotEmpty().Comment("large / small"),
		field.String("model").MaxLen(128).Default("").Comment("vLLM 返回的模型名"),
		field.String("prompt_version").MaxLen(64).Default(""),
		field.String("prompt_hash").MaxLen(16).Default("").Comment("模板 sha256 前 12 位"),
		field.String("input_digest").MaxLen(64).Default("").Comment("输入 sha256"),
		field.Text("input_excerpt").Default("").Comment("输入前 2000 字"),
		field.Text("output").Default(""),
		field.String("status").MaxLen(16).NotEmpty(),
		field.Text("error").Default(""),
		field.Int("latency_ms").Default(0),
		field.Int("queue_ms").Default(0),
		field.Int("prompt_tokens").Default(0),
		field.Int("completion_tokens").Default(0),
		field.Int("attempt").Default(1),
		field.Int("decision_id").Optional().Nillable().Comment("agent_decisions.id"),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (LlmCallLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at"),
		index.Fields("trace_id"),
		index.Fields("decision_id"),
	}
}
