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

// ServiceInstance 服务心跳。注册中心不可用时，靠这张表判断进程是否还活着。
type ServiceInstance struct {
	ent.Schema
}

func (ServiceInstance) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "service_instance"}}
}

func (ServiceInstance) Fields() []ent.Field {
	return []ent.Field{
		field.String("service_name").MaxLen(64).NotEmpty(),
		field.String("instance_id").MaxLen(64).NotEmpty(),
		field.String("addr").MaxLen(128).Default(""),
		field.Time("heartbeat_at").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (ServiceInstance) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("service_name", "instance_id").Unique(),
		index.Fields("heartbeat_at"),
	}
}
