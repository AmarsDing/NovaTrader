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

// AuditLog 管理网关审计。登录、实盘开关、Kill Switch、手动下单、风控参数和策略批准都记在这里。
type AuditLog struct {
	ent.Schema
}

func (AuditLog) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "audit_log"}}
}

func (AuditLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("actor").MaxLen(64).NotEmpty(),
		field.String("role").MaxLen(16).Default(""),
		field.String("action").MaxLen(64).NotEmpty(),
		field.String("target").MaxLen(256).Default(""),
		field.Text("detail").Default(""),
		field.Bool("ok").Default(false),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (AuditLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at"),
		index.Fields("action", "created_at"),
	}
}
