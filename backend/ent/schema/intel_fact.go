package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IntelFact 从公告、研报正文抽出的金额、比例、日期。
// start、end 是 news_sentiment.content 的字符（rune）下标，左闭右开，用来回到原文。
type IntelFact struct {
	ent.Schema
}

func (IntelFact) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intel_fact"}}
}

func (IntelFact) Fields() []ent.Field {
	return []ent.Field{
		field.Int("news_id"),
		field.String("fact_type").MaxLen(16).Comment("amount / ratio / date"),
		field.Float("value").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "numeric(24,4)"}).
			Comment("金额为元，比例为百分数；日期为空"),
		field.String("unit").MaxLen(16).Default(""),
		field.String("text").MaxLen(128).Comment("原文片段；日期为 2006-01-02"),
		field.Int("start"),
		field.Int("end"),
	}
}

func (IntelFact) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("news_id"),
	}
}
