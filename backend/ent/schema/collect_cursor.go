package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CollectCursor 增量采集的水位，例如某个新闻源已发布到的最新时间。
// 事件发出成功后才前移，重启后从水位继续，不重复也不漏。
type CollectCursor struct {
	ent.Schema
}

func (CollectCursor) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "collect_cursor"}}
}

func (CollectCursor) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(64).NotEmpty().Comment("域:源，例如 news:eastmoney"),
		field.Text("value").Default(""),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(pgTimestamptz),
	}
}

func (CollectCursor) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("name").Unique(),
	}
}
