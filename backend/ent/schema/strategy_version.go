package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// StrategyVersion 策略参数版本。对齐 doc/NovaTrader智脑方案.md 4.3.2。
// 参数演进必须人工确认后才能把 is_active 设为 true。
type StrategyVersion struct {
	ent.Schema
}

func (StrategyVersion) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "strategy_version"}}
}

func (StrategyVersion) Fields() []ent.Field {
	return []ent.Field{
		field.String("version_name").MaxLen(64).NotEmpty(),
		field.JSON("config_snapshot", map[string]any{}),
		field.Text("backtest_report").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Bool("is_active").Default(false),
	}
}
