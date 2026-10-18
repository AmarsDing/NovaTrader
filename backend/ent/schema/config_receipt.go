package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// ConfigReceipt is an actual service-instance load acknowledgement. No config
// values or external error bodies are stored here. Old instances remain visible.
type ConfigReceipt struct{ ent.Schema }

func (ConfigReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.String("service").MaxLen(32),
		field.String("instance").MaxLen(64),
		field.String("scope").MaxLen(32),
		field.Int("revision").NonNegative(),
		field.String("status").MaxLen(32),
		field.String("detail").MaxLen(256).Default(""),
		field.Time("observed_at").Default(time.Now),
	}
}
func (ConfigReceipt) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("service", "instance", "scope").Unique(),
		index.Fields("observed_at"),
	}
}
