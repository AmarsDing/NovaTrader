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

// MorningBriefing 盘前晨报。对齐 sql/init_db.sql。
type MorningBriefing struct {
	ent.Schema
}

func (MorningBriefing) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "morning_briefings"}}
}

func (MorningBriefing) Fields() []ent.Field {
	return []ent.Field{
		field.Time("date").
			SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.JSON("summary", map[string]any{}).Optional(),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (MorningBriefing) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("date"),
	}
}
