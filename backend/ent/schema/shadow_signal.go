package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ShadowSignal 大模型研判的前向影子记录。M07 设计文档第 11 节：模型不进历史回测，只从上线起按研判后的真实走势记收益。
// 参考价为研判之后第一个开盘的交易日开盘价；ret_tN 为第 N 个交易日收盘 / 参考价 − 1。
type ShadowSignal struct {
	ent.Schema
}

func (ShadowSignal) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "shadow_signal"}}
}

func (ShadowSignal) Fields() []ent.Field {
	return []ent.Field{
		field.Int("decision_id").Comment("agent_decisions.id"),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.String("model").MaxLen(128).Default(""),
		field.String("prompt_version").MaxLen(64).Default(""),
		field.String("decision_type").MaxLen(50).Default(""),
		field.Float("score").Optional().Nillable().SchemaType(numericRatio).Comment("0–100"),
		field.Time("decided_at").SchemaType(pgTimestamptz),
		field.Time("ref_date").Optional().Nillable().SchemaType(pgDate),
		field.Float("ref_price").Optional().Nillable().SchemaType(numericPrice),
		field.Float("ret_t1").Optional().Nillable().SchemaType(numericRatio),
		field.Float("ret_t3").Optional().Nillable().SchemaType(numericRatio),
		field.Float("ret_t5").Optional().Nillable().SchemaType(numericRatio),
		field.String("status").MaxLen(16).Default("pending").Comment("pending / partial / done"),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(pgTimestamptz),
	}
}

func (ShadowSignal) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("decision_id").Unique(),
		index.Fields("status"),
		index.Fields("decided_at"),
	}
}
