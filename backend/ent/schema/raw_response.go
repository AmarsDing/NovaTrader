package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RawResponse 数据源原始响应，M01 FR-01-11。默认关闭；打开后保留 7 天，body 为 gzip。
type RawResponse struct {
	ent.Schema
}

func (RawResponse) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "raw_response"}}
}

func (RawResponse) Fields() []ent.Field {
	return []ent.Field{
		field.String("source").MaxLen(16).NotEmpty(),
		field.String("domain").MaxLen(32).Default(""),
		field.Text("request").Default("").Comment("URL 或参数，已脱敏"),
		field.Int("status").Default(0),
		field.Bytes("body").Optional(),
		field.Time("fetched_at").Default(time.Now).SchemaType(pgTimestamptz),
	}
}

func (RawResponse) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("fetched_at"),
		index.Fields("source", "fetched_at"),
	}
}
