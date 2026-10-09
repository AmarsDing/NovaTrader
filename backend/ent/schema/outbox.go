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

// Outbox 待投递事件。对齐 M00 设计第 3 节：业务事务同时写业务表和本表，后台再发到 NATS。
type Outbox struct {
	ent.Schema
}

func (Outbox) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "outbox"}}
}

func (Outbox) Fields() []ent.Field {
	return []ent.Field{
		field.String("event_id").MaxLen(64).NotEmpty().Unique(),
		field.String("trace_id").MaxLen(64).Optional().Default(""),
		field.String("subject").MaxLen(128).NotEmpty(),
		field.String("source").MaxLen(64).NotEmpty(),
		field.Bytes("payload"),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("published_at").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Int("attempts").Default(0),
		field.Text("last_error").Optional().Nillable(),
	}
}

func (Outbox) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("published_at", "created_at"),
	}
}
