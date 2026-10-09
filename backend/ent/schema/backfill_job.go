package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BackfillJob 补数任务，M01 FR-01-05、FR-01-09。按日期、标的、数据域补拉，写入走 upsert，可重复执行。
// status：pending、running、success、failed。
type BackfillJob struct {
	ent.Schema
}

func (BackfillJob) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "backfill_job"}}
}

func (BackfillJob) Fields() []ent.Field {
	return []ent.Field{
		field.String("domain").MaxLen(32).NotEmpty(),
		field.Strings("symbols").Optional().Comment("空为全市场"),
		field.Time("start_date").SchemaType(pgDate),
		field.Time("end_date").SchemaType(pgDate),
		field.String("source").MaxLen(16).Default("").Comment("指定源；空为按优先级"),
		field.String("status").MaxLen(16).Default("pending"),
		field.Int("total").Default(0).Comment("交易日数"),
		field.Int("done").Default(0),
		field.Int64("rows").Default(0).Comment("写入行数"),
		field.Text("error").Default(""),
		field.String("requested_by").MaxLen(32).Default(""),
		field.Time("created_at").Default(time.Now).Immutable().SchemaType(pgTimestamptz),
		field.Time("started_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Time("finished_at").Optional().Nillable().SchemaType(pgTimestamptz),
	}
}

func (BackfillJob) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
		index.Fields("created_at"),
	}
}
