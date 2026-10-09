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

// NewsSentiment 新闻与舆情。对齐 sql/init_db.sql，M03 追加了去重、归簇和评分字段。
// 一条原始情报一行。sentiment_score 取值 [-1, 1]，importance 取值 0–5。
// content 存规范化后的正文，intel_fact 的字符下标指向它。
type NewsSentiment struct {
	ent.Schema
}

func (NewsSentiment) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "news_sentiment"}}
}

func (NewsSentiment) Fields() []ent.Field {
	return []ent.Field{
		field.Text("news_title").Optional().Nillable(),
		field.Text("content").Optional().Nillable(),
		field.String("source").MaxLen(100).Optional().Nillable(),
		field.Time("publish_time").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Float("sentiment_score").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "numeric(6,4)"}).
			Comment("-1 到 1"),
		field.Int("importance").Default(0).Comment("0 到 5"),
		field.String("stock_code").MaxLen(16).Optional().Nillable().
			Comment("置信度最高的关联证券，600519.SH；原脚本字段名 stock_related"),
		field.String("kind").MaxLen(16).Default("news").
			Comment("flash / news / announcement / report / social"),
		field.String("source_id").MaxLen(128).Default("").Comment("源头编号"),
		field.Text("url").Default(""),
		field.String("content_hash").MaxLen(64).Optional().Nillable().Unique().
			Comment("规范化标题与正文的 sha256"),
		field.Strings("source_codes").Optional().Comment("源头自带的证券代码，重启补评分时要用"),
		field.Int64("simhash").Default(0),
		field.Int("cluster_id").Optional().Nillable(),
		field.String("event_type").MaxLen(32).Default("other"),
		field.Int("half_life_minutes").Default(0),
		field.String("scorer").MaxLen(16).Default("").Comment("rule / small / large"),
		field.Bool("degraded").Default(false).Comment("模型不可用，只有规则分"),
		field.String("status").MaxLen(16).Default("scored").Comment("pending / scored / filtered"),
		field.Text("reason").Default("").Comment("打分理由或过滤原因"),
		field.Bool("publish_time_guessed").Default(false).Comment("源头没给发布时间，用了接收时间"),
		field.Time("received_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("scored_at").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (NewsSentiment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("publish_time"),
		index.Fields("stock_code"),
		index.Fields("cluster_id"),
		index.Fields("status"),
		index.Fields("received_at"),
		index.Fields("source", "source_id"),
	}
}
