package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SourceHealth 数据源健康，M01 FR-01-08。每个「数据域 + 源」一行，datahub 每分钟刷新。
// state：closed 正常、open 断开、half_open 探测中、disabled 已关闭、unavailable 依赖未就绪。
type SourceHealth struct {
	ent.Schema
}

func (SourceHealth) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "source_health"}}
}

func (SourceHealth) Fields() []ent.Field {
	return []ent.Field{
		field.String("domain").MaxLen(32).NotEmpty(),
		field.String("source").MaxLen(16).NotEmpty(),
		field.Int("priority").Default(0).Comment("0 为主源"),
		field.Bool("enabled").Default(true),
		field.Bool("official").Default(true),
		field.String("state").MaxLen(16).Default("closed"),
		field.Int("consecutive_failures").Default(0),
		field.Int64("success_count").Default(0),
		field.Int64("failure_count").Default(0),
		field.Int64("last_latency_ms").Default(0),
		field.Int64("avg_latency_ms").Default(0),
		field.Time("last_success_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Time("last_failure_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Text("last_error").Default(""),
		field.Int64("quota_used").Default(0).Comment("当日已用次数"),
		field.Int64("quota_limit").Default(0).Comment("当日上限，0 为不限"),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(pgTimestamptz),
	}
}

func (SourceHealth) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("domain", "source").Unique(),
	}
}
