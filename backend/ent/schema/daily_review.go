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

// DailyReview 盘后复盘。对齐 sql/init_db.sql。
type DailyReview struct {
	ent.Schema
}

func (DailyReview) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "daily_reviews"}}
}

func (DailyReview) Fields() []ent.Field {
	return []ent.Field{
		field.Time("date").
			SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.Text("review_content").Optional().Nillable(),
		field.JSON("accuracy_stats", map[string]any{}).Optional(),
		field.Strings("lessons_learned").Optional(),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (DailyReview) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("date"),
	}
}
