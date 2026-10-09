package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// KbDocument 知识库文档，一行是一份文档的一个版本。对齐 M04 设计第 2 节。
// content_hash 唯一，重复入库返回原文档；同一 source 再入新内容时旧版本标成 superseded。
type KbDocument struct {
	ent.Schema
}

func (KbDocument) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "kb_document"}}
}

func (KbDocument) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("category").Values("case", "rule", "compliance", "review"),
		field.String("title").MaxLen(256).NotEmpty(),
		field.String("source").MaxLen(1024).NotEmpty().Comment("文件路径、URL 或 signal:<book>:<id>"),
		field.Enum("source_type").Values("file", "url", "signal", "manual"),
		field.String("author").MaxLen(128).Default(""),
		field.String("format").MaxLen(16).Comment("md、txt、docx、xlsx、pdf"),
		field.String("file_name").MaxLen(256).Default(""),
		field.Int64("size_bytes").Default(0),
		field.String("content_hash").MaxLen(64).NotEmpty().Unique().Comment("原始字节 SHA-256"),
		field.Enum("status").Values("active", "superseded", "expired", "deleted").Default("active"),
		field.Int("version").Default(1),
		field.Strings("stock_codes").Optional(),
		field.Strings("tags").Optional(),
		field.Time("expires_at").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Int("chunk_count").Default(0),
		field.Enum("embed_status").Values("pending", "done").Default("pending"),
		field.Time("superseded_at").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("created_at").Default(time.Now).Immutable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (KbDocument) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("chunks", KbChunk.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("cases", KbCase.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
	}
}

func (KbDocument) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("source", "status"),
		index.Fields("category", "status"),
		index.Fields("status", "updated_at"),
	}
}
