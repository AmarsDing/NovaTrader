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

// AlertRule 盯盘价格告警规则（G3，对照 gloomberb alert-engine）：above/below/crosses 三态，
// crosses 以 last_price 为基线。触发后不再响，直到 Edit/Rearm 白名单重建（触发态字段清零，防幽灵基线）。
// 评估由 risk 服务在行情快照节拍上执行，触发行与 notify.request 经同一事务 + outbox 落库。
type AlertRule struct {
	ent.Schema
}

func (AlertRule) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "alert_rule"}}
}

func (AlertRule) Fields() []ent.Field {
	return []ent.Field{
		field.String("rule_id").MaxLen(48).NotEmpty().Unique().Immutable().Comment("alr-<uuid>，前端与事件都用它"),
		field.String("symbol").MaxLen(16).NotEmpty().Comment("600519.SH"),
		field.String("condition").MaxLen(12).NotEmpty().Comment("above / below / crosses"),
		field.Float("target_price").SchemaType(numericPrice).Comment("触发价"),
		field.String("note").MaxLen(200).Optional().Default(""),
		field.String("status").MaxLen(12).NotEmpty().Default("active").Comment("active / triggered"),
		field.Float("last_price").SchemaType(numericPrice).Optional().Nillable().Comment("crosses 的基线价；每个评估节拍刷新"),
		field.Time("armed_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).Comment("上次创建或重建时刻"),
		field.Time("triggered_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).Optional().Nillable(),
		field.String("operator").MaxLen(64).Optional().Default("").Comment("最后操作人（CRUD 归因列）；节拍落库与触发判定不改写本列"),
		field.Time("created_at").Default(time.Now).Immutable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (AlertRule) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "symbol"),
	}
}
