package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// ResearchJob is the durable M07 isolated research lifecycle. Immutable inputs
// bind the N4 manifest; no trade credentials, strategy publication or order port.
type ResearchJob struct{ ent.Schema }

func (ResearchJob) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "research_job"}}
}
func (ResearchJob) Fields() []ent.Field {
	return []ent.Field{
		field.String("run_id").MaxLen(80).NotEmpty().Unique().Immutable(),
		field.String("kind").MaxLen(32).NotEmpty().Immutable(),
		field.String("manifest_id").MaxLen(80).NotEmpty().Immutable(),
		field.String("manifest_hash").MaxLen(64).NotEmpty().Immutable(),
		field.Time("as_of").SchemaType(pgTimestamptz).Immutable(),
		field.Text("input_json").Immutable(),
		field.Text("options_json").Default("{}").Immutable(),
		field.String("input_hash").MaxLen(64).NotEmpty().Immutable(),
		field.Int64("seed").Default(0).Immutable(),
		field.Int("timeout_seconds").Default(60).Immutable(),
		field.String("created_by").MaxLen(64).Default("local").Immutable(),
		field.String("status").MaxLen(32).Default("queued"),
		field.String("reason").MaxLen(256).Default(""),
		field.Bool("cancel_requested").Default(false),
		field.String("owner").MaxLen(64).Default(""),
		field.Time("lease_until").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Int64("revision").Default(1),
		field.Text("result_json").Default(""),
		field.String("artifact_hash").MaxLen(64).Default(""),
		field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz).Immutable(),
		field.Time("started_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Time("finished_at").Optional().Nillable().SchemaType(pgTimestamptz),
	}
}
func (ResearchJob) Indexes() []ent.Index {
	return []ent.Index{index.Fields("status", "id"), index.Fields("lease_until", "status")}
}
