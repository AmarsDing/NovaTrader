package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// EventDisposition 是对一条持久事件（outbox 记录）的人工处置留痕（N2-02 处置回执）。
// 留痕只做审计标注：不改 outbox 投递状态，不回放事件，不回改/删除留痕。
// 处置动作集合封闭；note 由网关侧限定长度并在审计中同步留痕。
type EventDisposition struct{ ent.Schema }

func (EventDisposition) Fields() []ent.Field {
	return []ent.Field{
		field.String("event_id").MaxLen(128).NotEmpty(),
		field.String("trace_id").MaxLen(128).Default(""),
		field.String("action").MaxLen(32),
		field.String("note").MaxLen(280).Default(""),
		field.String("actor").MaxLen(64),
		field.Time("created_at").Default(time.Now),
	}
}
func (EventDisposition) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("event_id"),
		index.Fields("trace_id"),
		index.Fields("created_at"),
	}
}
