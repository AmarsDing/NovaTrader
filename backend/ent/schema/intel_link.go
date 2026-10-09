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

// IntelLink 情报到个股或概念的关联，带置信度。低于阈值的不写入。
type IntelLink struct {
	ent.Schema
}

func (IntelLink) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intel_link"}}
}

func (IntelLink) Fields() []ent.Field {
	return []ent.Field{
		field.Int("news_id"),
		field.Int("cluster_id"),
		field.String("target_type").MaxLen(16).Comment("stock / concept"),
		field.String("target").MaxLen(64).Comment("600519.SH 或概念名"),
		field.Float("confidence").
			SchemaType(map[string]string{dialect.Postgres: "numeric(5,4)"}),
		field.String("method").MaxLen(16).Comment("source / code / name / alias / concept"),
		field.String("matched").MaxLen(64).Default("").Comment("命中的原文片段"),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (IntelLink) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("news_id", "target_type", "target").Unique(),
		index.Fields("target_type", "target", "created_at"),
		index.Fields("cluster_id"),
		index.Fields("created_at"),
	}
}
