package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// ExecutionIntent is the durable identity before any risk call. A crashed or
// ambiguous submission is reconciled by the original client_order_id only.
type ExecutionIntent struct{ ent.Schema }

func (ExecutionIntent) Fields() []ent.Field {
	return []ent.Field{
		field.String("business_key").MaxLen(180).NotEmpty().Unique(),
		field.String("client_order_id").MaxLen(64).NotEmpty().Unique(),
		field.String("request_hash").MaxLen(64).NotEmpty(), field.String("book").MaxLen(16).NotEmpty(),
		field.String("plan_id").Default("").MaxLen(64), field.Int("plan_revision").Default(0),
		field.String("state").Default("claimed").MaxLen(32), field.String("reason").Default("").MaxLen(256),
		field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(pgTimestamptz),
	}
}
func (ExecutionIntent) Indexes() []ent.Index { return []ent.Index{index.Fields("book", "state")} }
