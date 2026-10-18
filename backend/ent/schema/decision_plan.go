package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// DecisionPlan stores immutable decision/conditional-plan revisions and their
// separately mutable authorization state. M06/M09 N5-01, never model orders.
type DecisionPlan struct{ ent.Schema }

func (DecisionPlan) Fields() []ent.Field {
	return []ent.Field{
		field.String("plan_id").MaxLen(64).NotEmpty(), field.Int("revision").Positive(),
		field.String("book").MaxLen(16).NotEmpty(), field.Int("signal_id").Positive(),
		field.JSON("snapshot", json.RawMessage{}), field.JSON("plan", json.RawMessage{}),
		field.String("state").Default("draft").MaxLen(32), field.String("mode").Default("review").MaxLen(32),
		field.String("operator").Default("").MaxLen(64), field.String("reason").Default("").MaxLen(256),
		field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(pgTimestamptz),
	}
}
func (DecisionPlan) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("plan_id", "revision").Unique(), index.Fields("book", "state"), index.Fields("signal_id"),
	}
}
