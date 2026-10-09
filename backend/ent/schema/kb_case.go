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

// KbCase 信号案例的结构化部分。对齐 M04 设计第 2、6 节。
// 正文另存为 category=case 的 kb_document；(book, signal_id) 唯一，重复事件不多建。
type KbCase struct {
	ent.Schema
}

func (KbCase) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "kb_case"}}
}

func (KbCase) Fields() []ent.Field {
	return []ent.Field{
		field.String("book").MaxLen(16).Default("paper").Comment("paper 模拟盘，live 实盘"),
		field.Int64("signal_id"),
		field.String("stock_code").MaxLen(16).NotEmpty(),
		field.String("stock_name").MaxLen(32).Default(""),
		field.String("strategy").MaxLen(64).Default(""),
		field.String("pattern").MaxLen(64).Default(""),
		field.String("emotion_phase").MaxLen(32).Default(""),
		field.String("signal_type").MaxLen(16).Default(""),
		field.Enum("final_status").Values("closed", "expired", "cancelled"),
		field.Time("signal_time").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("closed_at").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Float("entry_price").Optional().Nillable().SchemaType(numericPrice),
		field.Float("exit_price").Optional().Nillable().SchemaType(numericPrice),
		field.Float("pnl_pct").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "numeric(10,4)"}).
			Comment("百分比，3.25 表示 +3.25%"),
		field.Int("holding_days").Optional().Nillable(),
		field.Enum("outcome").Values("win", "loss", "flat", "unfilled", "cancelled"),
		field.String("attribution").MaxLen(32).Default(""),
		field.Int("doc_id").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (KbCase) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("document", KbDocument.Type).
			Ref("cases").
			Field("doc_id").
			Unique(),
	}
}

func (KbCase) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("book", "signal_id").Unique(),
		index.Fields("closed_at"),
		index.Fields("stock_code", "closed_at"),
		index.Fields("pattern", "emotion_phase"),
	}
}
