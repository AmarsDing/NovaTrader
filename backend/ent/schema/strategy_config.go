package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// StrategyConfig 策略动态参数。对齐 doc/NovaTrader智脑方案.md 4.1.4。
// 初始参数由业务启动写入，不在迁移里插数据。
type StrategyConfig struct {
	ent.Schema
}

func (StrategyConfig) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "strategy_config"}}
}

func (StrategyConfig) Fields() []ent.Field {
	return []ent.Field{
		field.String("config_key").MaxLen(64).NotEmpty().Unique(),
		field.Text("config_value").NotEmpty(),
		field.String("value_type").MaxLen(20).Default("string"),
		field.Text("description").Optional().Nillable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}
