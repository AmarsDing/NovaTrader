package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// HotRank 舆情热榜截面，由 M01 每 5 分钟写入，默认关闭。
type HotRank struct {
	ent.Schema
}

func (HotRank) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "hot_rank"}}
}

func (HotRank) Fields() []ent.Field {
	return []ent.Field{
		field.String("board").MaxLen(32).NotEmpty().Comment("榜单，例如 eastmoney_popularity"),
		field.Time("snap_time").SchemaType(pgTimestamptz),
		field.Int("rank"),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Float("heat").Optional().Nillable(),
		field.String("source").MaxLen(16).Default(""),
	}
}

func (HotRank) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("board", "snap_time", "rank").Unique(),
		index.Fields("symbol", "snap_time"),
	}
}
