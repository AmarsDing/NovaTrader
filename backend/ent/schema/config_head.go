package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"time"
)

// ConfigHead serializes configuration saves across processes. Revision is a
// StrategyVersion ID; singleton id=1 is never removed during rollback.
type ConfigHead struct{ ent.Schema }

func (ConfigHead) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id"),
		field.Int("revision").Default(0).NonNegative(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
