package schema

import (
	"time"

	"server/pkg/pgext"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// KbChunk 知识库分块。对齐 M04 设计第 2、4 节。
// tsv 由 Go 侧分词生成（数据库没有中文分词）；embedding 维度固定 1024，换维度要追加新列。
type KbChunk struct {
	ent.Schema
}

func (KbChunk) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "kb_chunk"}}
}

func (KbChunk) Fields() []ent.Field {
	return []ent.Field{
		field.Int("doc_id"),
		field.Int("seq"),
		field.Text("heading").Default(""),
		field.Text("content").NotEmpty(),
		field.Int("token_count").Default(0),
		field.Other("tsv", pgext.TSVector("")).
			SchemaType(map[string]string{dialect.Postgres: "tsvector"}),
		field.Other("embedding", pgext.Vector{}).Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "vector(1024)"}),
		field.String("embed_model").MaxLen(128).Default(""),
		field.Time("embedded_at").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("created_at").Default(time.Now).Immutable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (KbChunk) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("document", KbDocument.Type).
			Ref("chunks").
			Field("doc_id").
			Unique().
			Required(),
	}
}

func (KbChunk) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("doc_id", "seq").Unique(),
		index.Fields("embed_model"),
		index.Fields("tsv").
			Annotations(entsql.IndexType("GIN")),
		index.Fields("embedding").
			Annotations(entsql.IndexType("hnsw"), entsql.OpClass("vector_cosine_ops")),
	}
}
