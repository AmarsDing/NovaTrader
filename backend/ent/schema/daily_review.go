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
		field.String("kind").MaxLen(16).Default("program").
			Comment("program 程序版（每日定稿自动生成）/ ai 模型版复盘"),
		field.Text("review_content").Optional().Nillable(),
		field.JSON("accuracy_stats", map[string]any{}).Optional().
			Comment("八格读数 JSON：涨跌家数、成交额均值比、封板率、最高板、昨涨停表现、晋级、两融、指数位置"),
		field.Strings("lessons_learned").Optional(),
		field.String("model").MaxLen(64).Default("").Optional().
			Comment("ai 版时记录模型名；program 版为空"),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (DailyReview) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("date", "kind").Unique(),
	}
}
