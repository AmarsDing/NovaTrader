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

// RiskEvent 风控状态变化流水（M08）：Kill Switch 触发与恢复、模式切换、熔断、参数修改。
// 启动时按 kind 取最后一行恢复 Kill Switch 和模式，所以只追加不修改。
type RiskEvent struct {
	ent.Schema
}

func (RiskEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "risk_event"}}
}

func (RiskEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("kind").MaxLen(16).NotEmpty().Comment("killswitch / mode / breaker / param / reject_rate"),
		field.Bool("active").Default(false).Comment("killswitch、breaker：是否处于触发状态"),
		field.String("mode").MaxLen(4).Optional().Default("").Comment("事件之后的模式 L0–L3"),
		field.String("source").MaxLen(32).Optional().Default(""),
		field.Text("reason").Optional().Default(""),
		field.String("operator").MaxLen(64).Optional().Default(""),
		field.JSON("detail", map[string]any{}).Optional(),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (RiskEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("kind", "created_at"),
	}
}
