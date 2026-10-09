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

// OrderTrail 委托状态流水。只追加，用来追溯每次状态变化。
type OrderTrail struct {
	ent.Schema
}

func (OrderTrail) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "order_trail"}}
}

func (OrderTrail) Fields() []ent.Field {
	return []ent.Field{
		field.String("client_order_id").MaxLen(64).NotEmpty(),
		field.String("status").MaxLen(16).NotEmpty(),
		field.String("note").MaxLen(200).Default(""),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (OrderTrail) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("client_order_id", "created_at"),
	}
}
