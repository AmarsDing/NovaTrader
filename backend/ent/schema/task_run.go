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

// TaskRun 调度执行记录。同一任务的同一计划时刻可以有多次重试。
type TaskRun struct {
	ent.Schema
}

func (TaskRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "task_run"}}
}

func (TaskRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("task_name").MaxLen(64).NotEmpty(),
		field.Time("slot").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Int("attempt").Default(1),
		field.String("status").MaxLen(16).Default("running"),
		field.Text("error").Optional().Nillable(),
		field.Time("started_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("finished_at").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (TaskRun) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("task_name", "slot"),
		index.Fields("status"),
	}
}
