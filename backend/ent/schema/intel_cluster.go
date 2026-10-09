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

// IntelCluster 情报事件簇。近似重复的新闻、公告归为一簇，簇首条负责评分和告警。
// 见 doc/开发文档/M03-情报处理/设计文档.md 第 4 节。
type IntelCluster struct {
	ent.Schema
}

func (IntelCluster) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intel_cluster"}}
}

func (IntelCluster) Fields() []ent.Field {
	return []ent.Field{
		field.Int("head_news_id").Optional().Nillable().Comment("簇首条 news_sentiment.id"),
		field.Text("title").Default(""),
		field.String("kind").MaxLen(16).Default("news"),
		field.String("event_type").MaxLen(32).Default("other"),
		field.Int("item_count").Default(1),
		field.Time("first_seen").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("last_seen").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Float("sentiment_score").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "numeric(6,4)"}),
		field.Int("importance").Default(0),
		field.Time("alerted_at").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).
			Comment("已发 intel.alert 的时间，每簇只发一次"),
	}
}

func (IntelCluster) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("first_seen"),
		index.Fields("event_type"),
	}
}
